package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

func ExternalMonitorConfig() model.ExternalMonitorConfig {
	promURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PROMETHEUS_URL")), "/")
	grafanaURL := strings.TrimRight(strings.TrimSpace(os.Getenv("GRAFANA_URL")), "/")
	return model.ExternalMonitorConfig{
		PrometheusURL:        promURL,
		PrometheusConfigured: promURL != "",
		GrafanaURL:           grafanaURL,
		GrafanaConfigured:    grafanaURL != "",
	}
}

func ListPrometheusAlerts() ([]model.PrometheusAlert, error) {
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Alerts []struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
				State       string            `json:"state"`
				ActiveAt    string            `json:"activeAt"`
				Value       string            `json:"value"`
			} `json:"alerts"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := prometheusGet("/api/v1/alerts", nil, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "" && payload.Status != "success" {
		return nil, errors.New(payload.Error)
	}
	items := make([]model.PrometheusAlert, 0, len(payload.Data.Alerts))
	for _, alert := range payload.Data.Alerts {
		items = append(items, model.PrometheusAlert{
			Name:        firstNonEmpty(alert.Labels["alertname"], alert.Labels["name"], "未命名告警"),
			State:       alert.State,
			Severity:    alert.Labels["severity"],
			Summary:     alert.Annotations["summary"],
			Description: alert.Annotations["description"],
			ActiveAt:    alert.ActiveAt,
			Value:       alert.Value,
			Labels:      alert.Labels,
			Annotations: alert.Annotations,
		})
	}
	return items, nil
}

func ListPrometheusMetrics(limit int) ([]model.PrometheusMetric, error) {
	var payload struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
		Error  string   `json:"error"`
	}
	if err := prometheusGet("/api/v1/label/__name__/values", nil, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "" && payload.Status != "success" {
		return nil, errors.New(payload.Error)
	}
	sort.Strings(payload.Data)
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if len(payload.Data) > limit {
		payload.Data = payload.Data[:limit]
	}
	items := make([]model.PrometheusMetric, 0, len(payload.Data))
	for _, name := range payload.Data {
		items = append(items, model.PrometheusMetric{Name: name})
	}
	return items, nil
}

func QueryPrometheus(query string) (any, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("PromQL 不能为空")
	}
	var payload map[string]any
	if err := prometheusGet("/api/v1/query", url.Values{"query": []string{query}}, &payload); err != nil {
		return nil, err
	}
	if status, _ := payload["status"].(string); status != "" && status != "success" {
		if msg, _ := payload["error"].(string); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, errors.New("Prometheus 查询失败")
	}
	return payload["data"], nil
}

func ListGrafanaDashboards(limit int) ([]model.GrafanaDashboard, error) {
	var payload []struct {
		UID         string   `json:"uid"`
		Title       string   `json:"title"`
		URI         string   `json:"uri"`
		URL         string   `json:"url"`
		FolderTitle string   `json:"folderTitle"`
		Tags        []string `json:"tags"`
	}
	if err := grafanaGet("/api/search", url.Values{"type": []string{"dash-db"}}, &payload); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	items := make([]model.GrafanaDashboard, 0, len(payload))
	for _, dashboard := range payload {
		if len(items) >= limit {
			break
		}
		items = append(items, model.GrafanaDashboard{
			UID:         dashboard.UID,
			Title:       dashboard.Title,
			URI:         dashboard.URI,
			URL:         externalAbsoluteURL(os.Getenv("GRAFANA_URL"), dashboard.URL),
			FolderTitle: dashboard.FolderTitle,
			Tags:        dashboard.Tags,
		})
	}
	return items, nil
}

func prometheusGet(path string, query url.Values, out any) error {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("PROMETHEUS_URL")), "/")
	if base == "" {
		return errors.New("Prometheus 未配置，请设置 PROMETHEUS_URL")
	}
	return externalMonitorGet(base, strings.TrimSpace(os.Getenv("PROMETHEUS_TOKEN")), path, query, out)
}

func grafanaGet(path string, query url.Values, out any) error {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("GRAFANA_URL")), "/")
	if base == "" {
		return errors.New("Grafana 未配置，请设置 GRAFANA_URL")
	}
	return externalMonitorGet(base, strings.TrimSpace(os.Getenv("GRAFANA_TOKEN")), path, query, out)
}

func externalMonitorGet(base string, token string, path string, query url.Values, out any) error {
	target := base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("外部监控接口返回 HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func externalAbsoluteURL(base string, path string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if path == "" || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return base + "/" + strings.TrimLeft(path, "/")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
