package service

import (
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
	base, err := hadoopBaseURL(ds)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 8 * time.Second}
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
