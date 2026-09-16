package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
}

// isAgentSSH keeps legacy Agent rows working while new nodes are stored as SSH
// with an explicit authentication mode.
func isAgentSSH(ds model.DataSource) bool {
	return strings.EqualFold(ds.Type, "agent") || (strings.EqualFold(ds.Type, "ssh") && strings.EqualFold(strings.TrimSpace(ds.Options["authMode"]), "agent"))
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
	return "http://" + host + ":" + port, nil
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
	resp, err := safeHTTPClient(12 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("Agent 返回 %s: %s", resp.Status, strings.TrimSpace(string(raw)))
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

func runAgentFileCheck(ds model.DataSource, path, pattern string) (agentCheckResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var result agentCheckResult
	err := callAgent(ctx, ds, http.MethodPost, "/v1/checks/file", map[string]any{"path": path, "namePattern": pattern, "modifiedToday": true}, &result)
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
