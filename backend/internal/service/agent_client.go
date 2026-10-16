package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"monitor-platform/internal/model"
)

type agentMetrics struct {
	cpu, load1, load5, memory, disk float64
	filesystems                     []FilesystemMetric
}

type agentCheckResult struct {
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exitCode"`
	Summary  string `json:"summary"`
	Details  string `json:"details"`
}

type AgentStatus struct {
	Status                string `json:"status"`
	Version               string `json:"version"`
	StartedAt             string `json:"startedAt"`
	IPWhitelistConfigured bool   `json:"ipWhitelistConfigured"`
	TLSEnabled            bool   `json:"tlsEnabled"`
}

// agentFingerprintMu guards the TOFU write of an agent certificate fingerprint.
var agentFingerprintMu sync.Mutex

// isAgentSSH keeps legacy Agent rows working while new nodes are stored as SSH
// with an explicit authentication mode.
func isAgentSSH(ds model.DataSource) bool {
	return strings.EqualFold(ds.Type, "agent") || (strings.EqualFold(ds.Type, "ssh") && strings.EqualFold(strings.TrimSpace(ds.Options["authMode"]), "agent"))
}

// isLocalSSH represents the server host itself. It is intentionally still an
// SSH-category data source so existing dashboards and file/script rules can
// select it, while execution remains local and never opens an SSH connection.
func isLocalSSH(ds model.DataSource) bool {
	return strings.EqualFold(ds.Type, "ssh") && strings.EqualFold(strings.TrimSpace(ds.Options["authMode"]), "local")
}

func agentBaseURL(ds model.DataSource) (string, error) {
	host := strings.TrimSpace(ds.Host)
	port := strings.TrimSpace(ds.Port)
	if host == "" || port == "" {
		return "", fmt.Errorf("Agent 地址和端口不能为空")
	}
	if err := validateOutboundAddress(host); err != nil {
		return "", err
	}
	// Agent 传输强制使用 HTTPS：令牌不再明文出现在网络上。证书以指纹方式
	// 固定（TOFU），首次连接自动记录，之后指纹变化即拒绝。
	return "https://" + host + ":" + port, nil
}

// agentCertFingerprint returns the SHA-256 fingerprint of a certificate DER
// (lowercase hex, no colons), matching the agent's --print-fingerprint output.
func agentCertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// verifyAgentCertificate enforces fingerprint pinning: an empty expected value
// is the trust-on-first-use state (the observed fingerprint is recorded by the
// caller after a successful exchange); any mismatch is rejected.
func verifyAgentCertificate(expected string, peer *x509.Certificate) error {
	if peer == nil {
		return errors.New("Agent 未提供 TLS 证书")
	}
	if strings.TrimSpace(expected) == "" {
		return nil
	}
	observed := agentCertFingerprint(peer)
	if !strings.EqualFold(observed, strings.TrimSpace(expected)) {
		return fmt.Errorf("Agent 证书指纹不匹配（已记录 %s，实际 %s），可能遭受中间人攻击；确认 Agent 证书确实更换后，请在数据源执行“重置证书指纹”", expected, observed)
	}
	return nil
}

// agentTLSClient builds the HTTP client used for Agent calls: egress-safe
// dialing plus TLS 1.2+ with certificate fingerprint pinning.
func agentTLSClient(ds model.DataSource) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialValidated(ctx, dialer, network, address)
	}
	expected := strings.ToLower(strings.TrimSpace(ds.Options["agentCertFingerprint"]))
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Hostname verification is replaced by fingerprint pinning below.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("Agent 未提供 TLS 证书")
			}
			return verifyAgentCertificate(expected, state.PeerCertificates[0])
		},
	}
	return &http.Client{Timeout: 12 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		return validateOutboundAddress(req.URL.Hostname())
	}}, nil
}

// storeAgentCertFingerprint persists the observed agent certificate fingerprint
// (TOFU). A concurrent first connection that already recorded it wins.
func storeAgentCertFingerprint(id, fingerprint string) error {
	agentFingerprintMu.Lock()
	defer agentFingerprintMu.Unlock()
	existing, err := GetDataSourceByID(id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(existing.Options["agentCertFingerprint"]) != "" {
		return nil
	}
	return setDataSourceOption(id, "agentCertFingerprint", fingerprint)
}

func callAgent(ctx context.Context, ds model.DataSource, method, path string, requestBody any, responseBody any) error {
	base, err := agentBaseURL(ds)
	if err != nil {
		return err
	}
	target, err := url.Parse(base + path)
	if err != nil {
		return err
	}
	var body io.Reader
	if requestBody != nil {
		raw, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(ds.Password) != "" {
		req.Header.Set("Authorization", "Bearer "+ds.Password)
	}
	client, err := agentTLSClient(ds)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "first record does not look like a TLS handshake") {
			return fmt.Errorf("连接 Agent 失败：对方未启用 TLS。Agent 传输已强制使用 HTTPS，请确认 Agent 已设置 TLS_ENABLED=true 并升级到支持 TLS 的版本")
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("Agent 返回 %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	// Trust-on-first-use: record the certificate fingerprint after a
	// successful exchange when none was pinned yet.
	if strings.TrimSpace(ds.Options["agentCertFingerprint"]) == "" && resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		_ = storeAgentCertFingerprint(ds.ID, agentCertFingerprint(resp.TLS.PeerCertificates[0]))
	}
	if responseBody != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(responseBody)
	}
	return nil
}

func TestAgentDataSource(ds model.DataSource) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var health struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := callAgent(ctx, ds, http.MethodGet, "/health", nil, &health); err != nil {
		return err
	}
	if !strings.EqualFold(health.Status, "ok") {
		return fmt.Errorf("Agent 健康检查未返回 ok")
	}
	if strings.TrimSpace(health.Version) == "" {
		return fmt.Errorf("Agent 版本过旧，缺少版本与安全状态上报；请升级 Agent 后重试")
	}
	return nil
}

func GetAgentStatus(sourceID string) (AgentStatus, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !isAgentSSH(ds) || !ds.Enabled {
		return AgentStatus{}, fmt.Errorf("SSH - Agent 数据源不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var status AgentStatus
	if err := callAgent(ctx, ds, http.MethodGet, "/health", nil, &status); err != nil {
		return AgentStatus{}, err
	}
	return status, nil
}

func collectAgentDataSourceMetrics(ds model.DataSource) (agentMetrics, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var response struct {
		CPU         float64            `json:"cpu"`
		Load1       float64            `json:"load1"`
		Load5       float64            `json:"load5"`
		Memory      float64            `json:"memory"`
		Disk        float64            `json:"disk"`
		Filesystems []FilesystemMetric `json:"filesystems"`
	}
	if err := callAgent(ctx, ds, http.MethodGet, "/v1/metrics", nil, &response); err != nil {
		return agentMetrics{}, err
	}
	return agentMetrics{cpu: response.CPU, load1: response.Load1, load5: response.Load5, memory: response.Memory, disk: response.Disk, filesystems: response.Filesystems}, nil
}

func runAgentFileCheck(ds model.DataSource, path, pattern string, allowEmpty bool) (agentCheckResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var result agentCheckResult
	err := callAgent(ctx, ds, http.MethodPost, "/v1/checks/file", map[string]any{"path": path, "namePattern": pattern, "modifiedToday": true, "allowEmpty": allowEmpty}, &result)
	return result, err
}

func runAgentScriptCheck(ds model.DataSource, command, directory string) (agentCheckResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var result agentCheckResult
	err := callAgent(ctx, ds, http.MethodPost, "/v1/checks/script", map[string]any{"command": command, "workingDirectory": directory, "timeoutSeconds": 30}, &result)
	return result, err
}

func agentResultDetails(result agentCheckResult) string {
	return "退出码：" + strconv.Itoa(result.ExitCode) + "\n" + truncateRuleResult(result.Details, 4000)
}
