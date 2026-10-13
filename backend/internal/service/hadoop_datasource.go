package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
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
		endpoints := hadoopEndpointURLs(ds.Options[item.key])
		if len(endpoints) == 0 {
			continue
		}
		healthCount := 0
		endpointDetails := make([]string, 0, len(endpoints))
		for _, endpoint := range endpoints {
			status, reason := check(endpoint, item.path)
			if status == "健康" {
				healthCount++
			}
			if reason == "" {
				reason = "健康"
			}
			endpointDetails = append(endpointDetails, endpoint+"（"+reason+"）")
		}
		summary := fmt.Sprintf("%d/%d 健康", healthCount, len(endpoints))
		if item.key == "nodeManagerUrl" {
			health.NodeManager = summary
		} else {
			health.JobHistory = summary
		}
		details = append(details, item.label+"："+strings.Join(endpointDetails, "、"))
	}
	health.Details = strings.Join(details, "；")
	_ = saveHadoopHealth(health)
	if health.ResourceManager != "健康" {
		return health, errors.New("Hadoop ResourceManager 不可用：" + health.Details)
	}
	if strings.HasPrefix(health.NodeManager, "0/") || strings.HasPrefix(health.JobHistory, "0/") {
		return health, errors.New("Hadoop 日志服务异常：" + health.Details)
	}
	return health, nil
}

// hadoopEndpointURLs parses the multi-node endpoints accepted by the data
// source form. A comma, semicolon, or newline separates independent services.
func hadoopEndpointURLs(value string) []string {
	seen := make(map[string]struct{})
	items := make([]string, 0)
	for _, raw := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }) {
		endpoint := strings.TrimRight(strings.TrimSpace(raw), "/")
		if endpoint == "" {
			continue
		}
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			endpoint = "http://" + endpoint
		}
		if _, err := url.ParseRequestURI(endpoint); err != nil {
			continue
		}
		if _, exists := seen[endpoint]; exists {
			continue
		}
		seen[endpoint] = struct{}{}
		items = append(items, endpoint)
	}
	return items
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
