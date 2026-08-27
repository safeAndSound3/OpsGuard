package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

func TestPrometheusDataSource(ds model.DataSource) error {
	_, err := QueryPrometheusDataSource(ds, "up")
	return err
}

func ListPrometheusMetricNames(sourceID string, limit int) ([]model.PrometheusMetric, error) {
	ds, err := prometheusDataSourceWithSecret(sourceID)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
		Error  string   `json:"error"`
	}
	if err := prometheusDataSourceGet(ds, "/api/v1/label/__name__/values", nil, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "" && payload.Status != "success" {
		return nil, errors.New(payload.Error)
	}
	sort.Strings(payload.Data)
	if limit <= 0 || limit > 1000 {
		limit = 300
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

func QueryPrometheusDataSourceByID(sourceID string, query string) (any, error) {
	ds, err := prometheusDataSourceWithSecret(sourceID)
	if err != nil {
		return nil, err
	}
	return QueryPrometheusDataSource(ds, query)
}

func QueryPrometheusDataSource(ds model.DataSource, query string) (any, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("PromQL 不能为空")
	}
	var payload map[string]any
	if err := prometheusDataSourceGet(ds, "/api/v1/query", url.Values{"query": []string{query}}, &payload); err != nil {
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

func ListPrometheusDataSourceAlerts(sourceID string) ([]model.PrometheusAlert, error) {
	ds, err := prometheusDataSourceWithSecret(sourceID)
	if err != nil {
		return nil, err
	}
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
	if err := prometheusDataSourceGet(ds, "/api/v1/alerts", nil, &payload); err != nil {
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

func prometheusDataSourceWithSecret(sourceID string) (model.DataSource, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return model.DataSource{}, errors.New("Prometheus 数据源不存在")
	}
	if !ds.Enabled {
		return model.DataSource{}, errors.New("Prometheus 数据源已停用")
	}
	if !strings.EqualFold(ds.Type, "prometheus") {
		return model.DataSource{}, errors.New("请选择 Prometheus 数据源")
	}
	_ = FillDataSourcePassword(&ds)
	return ds, nil
}

func prometheusDataSourceGet(ds model.DataSource, path string, query url.Values, out any) error {
	base := prometheusBaseURL(ds)
	if base == "" {
		return errors.New("Prometheus 地址不能为空")
	}
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
	if token := strings.TrimSpace(ds.Password); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Prometheus 返回 HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func prometheusBaseURL(ds model.DataSource) string {
	host := strings.TrimRight(strings.TrimSpace(ds.Host), "/")
	port := strings.TrimSpace(ds.Port)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		parsed, err := url.Parse(host)
		if err == nil && parsed.Port() == "" && port != "" {
			parsed.Host = parsed.Host + ":" + port
			return strings.TrimRight(parsed.String(), "/")
		}
		return host
	}
	if port == "" {
		port = "9090"
	}
	return "http://" + host + ":" + port
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
