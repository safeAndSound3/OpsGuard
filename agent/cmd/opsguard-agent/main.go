package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

type config struct {
	listen, token      string
	allowedPlatformIPs []netip.Prefix
	timeout            time.Duration
	maxOutput          int64
}
type collector struct{ total, idle uint64 }
type fileCheck struct {
	Path          string `json:"path"`
	NamePattern   string `json:"namePattern"`
	ModifiedToday bool   `json:"modifiedToday"`
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
	flag.Parse()
	cfg, err := loadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	metrics := &collector{}
	startedAt := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "version": version, "startedAt": startedAt.Format(time.RFC3339), "ipWhitelistConfigured": len(cfg.allowedPlatformIPs) > 0})
	})
	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r, cfg.token) {
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
		if !auth(r, cfg.token) {
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
		if !auth(r, cfg.token) {
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
	log.Printf("agent listening on %s", cfg.listen)
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
	cfg := config{listen: values["LISTEN"], token: values["TOKEN"], timeout: 30 * time.Second, maxOutput: 65536}
	if cfg.listen == "" {
		cfg.listen = "0.0.0.0:4001"
	}
	if cfg.token == "" {
		return config{}, errors.New("TOKEN is required")
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
func auth(r *http.Request, token string) bool {
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(token)) == 1
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
	out, err := exec.Command("df", "-P", "-B1").Output()
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
	if mount == "/var/lib/docker" || strings.HasPrefix(mount, "/var/lib/docker/") || mount == "/var/lib/kubelet" || strings.HasPrefix(mount, "/var/lib/kubelet/") {
		return true
	}
	for _, value := range []string{"tmpfs", "devtmpfs", "overlay", "squashfs", "nsfs", "proc", "sysfs", "cgroup", "cgroup2"} {
		if fs == value || strings.HasPrefix(fs, value+":") {
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
	matchCount, scanned := 0, 0
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
			line := entry.Name() + "\n"
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
	if truncated {
		summary += " (results truncated)"
	}
	return checkResult{Passed: matchCount > 0, Summary: summary, Details: strings.TrimSuffix(details.String(), "\n")}
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
	out, e := cmd.CombinedOutput()
	code := 0
	if e != nil {
		if exit, ok := e.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			return checkResult{Summary: "command start failed", Details: e.Error()}
		}
	}
	if int64(len(out)) > cfg.maxOutput {
		out = append(out[:cfg.maxOutput], []byte("\n... truncated")...)
	}
	return checkResult{Passed: code == 0, ExitCode: code, Summary: fmt.Sprintf("exit code %d", code), Details: string(out)}
}
