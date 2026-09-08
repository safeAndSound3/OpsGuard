package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

// TestHadoopDataSource supports the standard Hadoop 3 web endpoints without
// requiring SSH access: ResourceManager, NameNode, or a reachable web UI.
func TestHadoopDataSource(ds model.DataSource) (string, error) {
	_, err := CheckHadoopHealth(ds)
	if err != nil {
		return "", err
	}
	return "ResourceManager", nil
}

func legacyTestHadoopDataSource(ds model.DataSource) (string, error) {
	base, err := hadoopBaseURL(ds)
	if err != nil {
		return "", err
	}
	client := safeHTTPClient(8 * time.Second)
	for _, candidate := range []struct {
		path string
		role string
	}{
		{path: "/ws/v1/cluster/info", role: "ResourceManager"},
		{path: "/jmx", role: "NameNode"},
		{path: "", role: "Web UI"},
	} {
		response, requestErr := client.Get(strings.TrimRight(base, "/") + candidate.path)
		if requestErr != nil {
			continue
		}
		_ = response.Body.Close()
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			return candidate.role, nil
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("Hadoop Web 地址可达，但当前需要认证（HTTP %d）", response.StatusCode)
		}
	}
	return "", errors.New("无法访问 Hadoop Web 地址，请确认地址、端口和网络连通性")
}

func CheckHadoopHealth(ds model.DataSource) (model.HadoopHealth, error) {
	base, err := hadoopBaseURL(ds)
	if err != nil {
		return model.HadoopHealth{}, err
	}
	health := model.HadoopHealth{SourceID: ds.ID, NodeManager: "未配置", JobHistory: "未配置", CheckedAt: time.Now().Format("2006-01-02 15:04:05")}
	client := safeHTTPClient(8 * time.Second)
	check := func(target, path string) (string, string) {
		resp, reqErr := client.Get(strings.TrimRight(target, "/") + path)
		if reqErr != nil {
			return "异常", reqErr.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return "健康", ""
		}
		return "异常", fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	var errText string
	health.ResourceManager, errText = check(base, "/ws/v1/cluster/info")
	details := []string{"ResourceManager：" + errText}
	if health.ResourceManager == "健康" {
		details[0] = "ResourceManager：健康"
	}
	for _, item := range []struct{ key, label, path string }{{"nodeManagerUrl", "NodeManager", "/ws/v1/node/info"}, {"jobHistoryUrl", "JobHistory", "/ws/v1/history/info"}} {
		target := strings.TrimRight(strings.TrimSpace(ds.Options[item.key]), "/")
		if target == "" {
			continue
		}
		status, reason := check(target, item.path)
		if item.key == "nodeManagerUrl" {
			health.NodeManager = status
		} else {
			health.JobHistory = status
		}
		if reason == "" {
			reason = "健康"
		}
		details = append(details, item.label+"："+reason)
	}
	health.Details = strings.Join(details, "；")
	_ = saveHadoopHealth(health)
	if health.ResourceManager != "健康" {
		return health, errors.New("Hadoop ResourceManager 不可用：" + health.Details)
	}
	if health.NodeManager == "异常" || health.JobHistory == "异常" {
		return health, errors.New("Hadoop 日志服务异常：" + health.Details)
	}
	return health, nil
}

func saveHadoopHealth(health model.HadoopHealth) error {
	current := currentStore()
	if current == nil || health.SourceID == "" {
		return nil
	}
	_, err := current.ExecContext(context.Background(), `INSERT INTO hadoop_health_checks (source_id, resource_manager, node_manager, job_history, details, checked_at) VALUES (?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE resource_manager=VALUES(resource_manager), node_manager=VALUES(node_manager), job_history=VALUES(job_history), details=VALUES(details), checked_at=VALUES(checked_at)`, health.SourceID, health.ResourceManager, health.NodeManager, health.JobHistory, health.Details, health.CheckedAt)
	return err
}

func GetHadoopHealth(sourceID string) (model.HadoopHealth, error) {
	current := currentStore()
	if current == nil {
		return model.HadoopHealth{}, errors.New("Hadoop health store is not initialized")
	}
	var health model.HadoopHealth
	err := current.QueryRow(`SELECT source_id, resource_manager, node_manager, job_history, details, checked_at FROM hadoop_health_checks WHERE source_id = ?`, sourceID).Scan(&health.SourceID, &health.ResourceManager, &health.NodeManager, &health.JobHistory, &health.Details, &health.CheckedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.HadoopHealth{}, errors.New("Hadoop 尚未检测")
	}
	return health, err
}

func hadoopBaseURL(ds model.DataSource) (string, error) {
	host := strings.TrimSpace(ds.Host)
	if host == "" {
		return "", errors.New("Hadoop Web 地址不能为空")
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	parsed, err := url.Parse(host)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("Hadoop Web 地址格式不正确")
	}
	if parsed.Port() == "" {
		if port := strings.TrimSpace(ds.Port); port != "" {
			parsed.Host = net.JoinHostPort(parsed.Hostname(), port)
		}
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}
