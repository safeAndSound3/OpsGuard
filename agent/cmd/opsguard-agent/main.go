package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var version = "dev"

type config struct {
	listen             string
	tokens             []string
	allowedPlatformIPs []netip.Prefix
	timeout            time.Duration
	maxOutput          int64
	logFile            string
	tlsEnabled         bool
	tlsCertFile        string
	tlsKeyFile         string
}
type collector struct {
	mu          sync.Mutex
	total, idle uint64
}
type fileCheck struct {
	Path          string `json:"path"`
	NamePattern   string `json:"namePattern"`
	ModifiedToday bool   `json:"modifiedToday"`
	AllowEmpty    bool   `json:"allowEmpty"`
}
type scriptCheck struct {
	Command          string `json:"command"`
	WorkingDirectory string `json:"workingDirectory"`
	TimeoutSeconds   int    `json:"timeoutSeconds"`
}
type checkResult struct {
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exitCode,omitempty"`
	Summary  string `json:"summary"`
	Details  string `json:"details"`
}
type filesystemMetric struct {
	Filesystem  string  `json:"filesystem"`
	MountPoint  string  `json:"mountPoint"`
	TotalBytes  int64   `json:"totalBytes"`
	UsedBytes   int64   `json:"usedBytes"`
	UsedPercent float64 `json:"usedPercent"`
}
type metricsResponse struct {
	Up          float64            `json:"up"`
	CPU         float64            `json:"cpu"`
	Load1       float64            `json:"load1"`
	Load5       float64            `json:"load5"`
	Memory      float64            `json:"memory"`
	Disk        float64            `json:"disk"`
	Filesystems []filesystemMetric `json:"filesystems"`
}

func main() {
	path := flag.String("config", "/etc/opsguard-agent/agent.conf", "path to agent .conf")
	printFingerprint := flag.Bool("print-fingerprint", false, "print the TLS certificate SHA-256 fingerprint (generating it if needed) and exit")
	flag.Parse()
	cfg, err := loadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	if *printFingerprint {
		fingerprint, err := agentCertificateFingerprint(cfg)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("SHA256 fingerprint: %s\n", fingerprint)
		fmt.Printf("TLS cert: %s\nTLS key: %s\n", cfg.tlsCertFile, cfg.tlsKeyFile)
		return
	}
	if closeLog, err := configureFileLogging(cfg.logFile); err != nil {
		log.Printf("无法打开 Agent 日志文件 %s：%v，继续输出到终端", cfg.logFile, err)
	} else {
		defer closeLog()
	}
	metrics := &collector{}
	startedAt := time.Now()
	if cfg.tlsEnabled {
		fingerprint, certErr := agentCertificateFingerprint(cfg)
		if certErr != nil {
			log.Fatal(certErr)
		}
		log.Printf("TLS enabled: cert=%s key=%s fingerprint=%s", cfg.tlsCertFile, cfg.tlsKeyFile, fingerprint)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "version": version, "startedAt": startedAt.Format(time.RFC3339), "ipWhitelistConfigured": len(cfg.allowedPlatformIPs) > 0, "tlsEnabled": cfg.tlsEnabled})
	})
	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r, cfg.tokens) {
			deny(w)
			return
		}
		writeJSON(w, 200, metrics.read())
	})
	mux.HandleFunc("/v1/checks/file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			method(w)
			return
		}
		if !auth(r, cfg.tokens) {
			deny(w)
			return
		}
		var req fileCheck
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req) != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid request"})
			return
		}
		result := runFile(req, cfg)
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("/v1/checks/script", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			method(w)
			return
		}
		if !auth(r, cfg.tokens) {
			deny(w)
			return
		}
		var req scriptCheck
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req) != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid request"})
			return
		}
		result := runScript(req, cfg)
		writeJSON(w, 200, result)
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedPlatform(r, cfg) {
			denySource(w)
			return
		}
		mux.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: cfg.listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second}
	log.Printf("agent listening on %s (tls=%v)", cfg.listen, cfg.tlsEnabled)
	if cfg.tlsEnabled {
		log.Fatal(server.ListenAndServeTLS(cfg.tlsCertFile, cfg.tlsKeyFile))
	}
	log.Fatal(server.ListenAndServe())
}

func loadConfig(path string) (config, error) {
	file, err := os.Open(path)
	if err != nil {
		return config{}, err
	}
	defer file.Close()
	values := map[string]string{}
	s := bufio.NewScanner(file)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "\"'")
		}
	}
	if s.Err() != nil {
		return config{}, s.Err()
	}
	configDir := filepath.Dir(path)
	cfg := config{listen: values["LISTEN"], timeout: 30 * time.Second, maxOutput: 65536, logFile: values["LOG_FILE"], tlsEnabled: strings.EqualFold(strings.TrimSpace(values["TLS_ENABLED"]), "true")}
	if strings.TrimSpace(cfg.logFile) == "" {
		cfg.logFile = filepath.Join(configDir, "..", "..", "logs", "agent", "agent.log")
	}
	if cfg.listen == "" {
		cfg.listen = "0.0.0.0:4001"
	}
	// TOKEN accepts a comma-separated list so tokens can be rotated without
	// losing connectivity: append the new token, restart, then remove the old one.
	for _, raw := range strings.Split(values["TOKEN"], ",") {
		if token := strings.TrimSpace(raw); token != "" {
			cfg.tokens = append(cfg.tokens, token)
		}
	}
	if len(cfg.tokens) == 0 {
		return config{}, errors.New("TOKEN is required")
	}
	cfg.tlsCertFile = strings.TrimSpace(values["TLS_CERT_FILE"])
	if cfg.tlsCertFile == "" {
		cfg.tlsCertFile = filepath.Join(configDir, "agent.crt")
	}
	cfg.tlsKeyFile = strings.TrimSpace(values["TLS_KEY_FILE"])
	if cfg.tlsKeyFile == "" {
		cfg.tlsKeyFile = filepath.Join(configDir, "agent.key")
	}
	for _, raw := range strings.Split(values["ALLOWED_PLATFORM_IPS"], ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if address, err := netip.ParseAddr(raw); err == nil {
			cfg.allowedPlatformIPs = append(cfg.allowedPlatformIPs, netip.PrefixFrom(address, address.BitLen()))
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return config{}, fmt.Errorf("ALLOWED_PLATFORM_IPS contains invalid IP or CIDR %q", raw)
		}
		cfg.allowedPlatformIPs = append(cfg.allowedPlatformIPs, prefix)
	}
	if v, e := strconv.Atoi(values["COMMAND_TIMEOUT_SECONDS"]); e == nil && v > 0 && v <= 300 {
		cfg.timeout = time.Duration(v) * time.Second
	}
	if v, e := strconv.ParseInt(values["MAX_OUTPUT_BYTES"], 10, 64); e == nil && v >= 1024 && v <= 1<<20 {
		cfg.maxOutput = v
	}
	return cfg, nil
}

func configureFileLogging(path string) (func(), error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return func() {}, errors.New("日志文件路径为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return func() {}, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return func() {}, err
	}
	log.SetOutput(io.MultiWriter(os.Stdout, file))
	return func() { _ = file.Close() }, nil
}

// agentCertificateFingerprint ensures the TLS certificate exists (generating a
// self-signed one when missing) and returns its SHA-256 fingerprint in
// lowercase hex, matching the fingerprint the platform pins per data source.
func agentCertificateFingerprint(cfg config) (string, error) {
	if err := ensureAgentCertificate(cfg); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(cfg.tlsCertFile)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", errors.New("failed to decode TLS certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:]), nil
}

// ensureAgentCertificate generates a self-signed ECDSA certificate when either
// of the configured cert/key files is missing. Certificate validity is 10 years
// and the SANs cover the hostname and local addresses; because the platform
// pins the certificate fingerprint instead of hostnames, SANs are informational.
func ensureAgentCertificate(cfg config) error {
	certExists, keyExists := fileExists(cfg.tlsCertFile), fileExists(cfg.tlsKeyFile)
	if certExists && keyExists {
		return nil
	}
	if certExists || keyExists {
		return errors.New("Agent TLS 证书和私钥必须同时存在；请恢复缺失文件，确认换证后再重置平台保存的证书指纹")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.tlsCertFile), 0o750); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "opsguard-agent"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if hostname != "" {
		template.DNSNames = append(template.DNSNames, hostname)
	}
	for _, address := range localAddresses() {
		template.IPAddresses = append(template.IPAddresses, address)
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(cfg.tlsCertFile, certPEM, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(cfg.tlsKeyFile, keyPEM, 0o600); err != nil {
		return err
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func localAddresses() []net.IP {
	var result []net.IP
	interfaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if ipNet, ok := address.(*net.IPNet); ok && ipNet.IP.IsGlobalUnicast() && !ipNet.IP.IsLoopback() {
				result = append(result, ipNet.IP)
			}
		}
	}
	return result
}

func allowedPlatform(r *http.Request, cfg config) bool {
	if len(cfg.allowedPlatformIPs) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, prefix := range cfg.allowedPlatformIPs {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
func auth(r *http.Request, tokens []string) bool {
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if provided == "" {
		return false
	}
	for _, token := range tokens {
		if token != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1 {
			return true
		}
	}
	return false
}
func deny(w http.ResponseWriter) { writeJSON(w, 401, map[string]string{"error": "invalid token"}) }
func denySource(w http.ResponseWriter) {
	writeJSON(w, 403, map[string]string{"error": "source IP is not allowed"})
}
func method(w http.ResponseWriter) {
	writeJSON(w, 405, map[string]string{"error": "method not allowed"})
}
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func (c *collector) read() metricsResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := metricsResponse{Up: 1}
	if raw, e := os.ReadFile("/proc/loadavg"); e == nil {
		f := strings.Fields(string(raw))
		if len(f) > 1 {
			result.Load1, _ = strconv.ParseFloat(f[0], 64)
			result.Load5, _ = strconv.ParseFloat(f[1], 64)
		}
	}
	if raw, e := os.ReadFile("/proc/meminfo"); e == nil {
		t, a := mem(string(raw), "MemTotal"), mem(string(raw), "MemAvailable")
		if t > 0 {
			result.Memory = (1 - a/t) * 100
		}
	}
	var fs syscall.Statfs_t
	if syscall.Statfs("/", &fs) == nil && fs.Blocks > 0 {
		result.Disk = (1 - float64(fs.Bavail)/float64(fs.Blocks)) * 100
	}
	if raw, e := os.ReadFile("/proc/stat"); e == nil {
		f := strings.Fields(strings.SplitN(string(raw), "\n", 2)[0])
		if len(f) > 4 {
			total := uint64(0)
			for _, v := range f[1:] {
				n, _ := strconv.ParseUint(v, 10, 64)
				total += n
			}
			idle, _ := strconv.ParseUint(f[4], 10, 64)
			if c.total > 0 && total > c.total {
				result.CPU = (1 - float64(idle-c.idle)/float64(total-c.total)) * 100
			}
			c.total, c.idle = total, idle
		}
	}
	result.Filesystems = readFilesystems()
	return result
}
func readFilesystems() []filesystemMetric {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "df", "-P", "-B1").Output()
	if err != nil {
		return nil
	}
	items := []filesystemMetric{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || f[0] == "Filesystem" {
			continue
		}
		total, totalErr := strconv.ParseInt(f[1], 10, 64)
		used, usedErr := strconv.ParseInt(f[2], 10, 64)
		if totalErr != nil || usedErr != nil || total <= 0 {
			continue
		}
		mount := strings.Join(f[5:], " ")
		if excludedFilesystem(f[0], mount) {
			continue
		}
		items = append(items, filesystemMetric{Filesystem: f[0], MountPoint: mount, TotalBytes: total, UsedBytes: used, UsedPercent: float64(used) / float64(total) * 100})
	}
	return items
}
func excludedFilesystem(filesystem, mountPoint string) bool {
	fs := strings.ToLower(strings.TrimSpace(filesystem))
	mount := strings.TrimRight(strings.TrimSpace(mountPoint), "/")
	if mount == "" {
		mount = "/"
	}
	if containerRuntimeMount(mount) {
		return true
	}
	for _, value := range []string{"tmpfs", "devtmpfs", "overlay", "squashfs", "nsfs", "proc", "sysfs", "cgroup", "cgroup2"} {
		if fs == value || strings.HasPrefix(fs, value+":") {
			return true
		}
	}
	return false
}
func containerRuntimeMount(mountPoint string) bool {
	for _, segment := range strings.Split(strings.Trim(mountPoint, "/"), "/") {
		switch strings.ToLower(strings.TrimSpace(segment)) {
		case "docker", "containerd", "containers", "kubelet", "kubernetes", "podman", "k3s", "rancher":
			return true
		}
	}
	return false
}
func mem(input, key string) float64 {
	for _, line := range strings.Split(input, "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && strings.TrimSuffix(f[0], ":") == key {
			v, _ := strconv.ParseFloat(f[1], 64)
			return v * 1024
		}
	}
	return 0
}
func runFile(req fileCheck, cfg config) checkResult {
	if !filepath.IsAbs(req.Path) {
		return checkResult{Summary: "path must be absolute"}
	}
	pattern, e := regexp.Compile(req.NamePattern)
	if e != nil {
		return checkResult{Summary: "invalid pattern", Details: e.Error()}
	}
	directory, e := os.Open(req.Path)
	if e != nil {
		return checkResult{Summary: "read directory failed", Details: e.Error()}
	}
	defer directory.Close()
	const maxFileCheckEntries = 10000
	const maxFileCheckMatches = 1000
	matchCount, emptyCount, scanned := 0, 0, 0
	truncated := false
	var details strings.Builder
	today := time.Now().Format("2006-01-02")
	for scanned < maxFileCheckEntries {
		entries, readErr := directory.ReadDir(256)
		for _, entry := range entries {
			scanned++
			if entry.IsDir() || !pattern.MatchString(entry.Name()) {
				continue
			}
			info, err := entry.Info()
			if err != nil || req.ModifiedToday && info.ModTime().Format("2006-01-02") != today {
				continue
			}
			matchCount++
			line := entry.Name()
			if info.Size() == 0 {
				emptyCount++
				line += "（空文件）"
			}
			line += "\n"
			if matchCount > maxFileCheckMatches || int64(details.Len()+len(line)) > cfg.maxOutput {
				truncated = true
				continue
			}
			details.WriteString(line)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return checkResult{Summary: "read directory failed", Details: readErr.Error()}
		}
		if scanned >= maxFileCheckEntries {
			truncated = true
		}
	}
	summary := fmt.Sprintf("matched %d files", matchCount)
	if emptyCount > 0 && !req.AllowEmpty {
		summary += fmt.Sprintf("，empty %d files", emptyCount)
	}
	if truncated {
		summary += " (results truncated)"
	}
	return checkResult{Passed: matchCount > 0 && (req.AllowEmpty || emptyCount == 0), Summary: summary, Details: strings.TrimSuffix(details.String(), "\n")}
}
func runScript(req scriptCheck, cfg config) checkResult {
	if strings.TrimSpace(req.Command) == "" {
		return checkResult{Summary: "command is required"}
	}
	timeout := cfg.timeout
	if req.TimeoutSeconds > 0 && time.Duration(req.TimeoutSeconds)*time.Second < timeout {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", req.Command)
	// Each check has its own process group. A shell command can spawn children;
	// cancelling only the shell would otherwise leave those processes running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	if req.WorkingDirectory != "" {
		if !filepath.IsAbs(req.WorkingDirectory) {
			return checkResult{Summary: "workingDirectory must be absolute"}
		}
		cmd.Dir = req.WorkingDirectory
	}
	output := &limitedCommandOutput{limit: cfg.maxOutput}
	cmd.Stdout, cmd.Stderr = output, output
	e := cmd.Run()
	code := 0
	if e != nil {
		if exit, ok := e.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			return checkResult{Summary: "command start failed", Details: e.Error()}
		}
	}
	return checkResult{Passed: code == 0, ExitCode: code, Summary: fmt.Sprintf("exit code %d", code), Details: output.String()}
}

type limitedCommandOutput struct {
	mu        sync.Mutex
	data      []byte
	limit     int64
	truncated bool
}

func (w *limitedCommandOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	remaining := w.limit - int64(len(w.data))
	if remaining > 0 {
		if remaining > int64(n) {
			remaining = int64(n)
		}
		w.data = append(w.data, p[:int(remaining)]...)
	}
	if int64(n) > remaining {
		w.truncated = true
	}
	return n, nil
}

func (w *limitedCommandOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.truncated {
		return string(w.data) + "\n... truncated"
	}
	return string(w.data)
}
