package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"monitor-platform/internal/model"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func ambariBaseURL(ds model.DataSource) (string, error) {
	host := strings.TrimSpace(ds.Host)
	if host == "" {
		return "", errors.New("Ambari Web 地址不能为空")
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	parsed, err := url.Parse(host)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("Ambari Web 地址格式不正确")
	}
	if parsed.Port() == "" && strings.TrimSpace(ds.Port) != "" {
		parsed.Host = net.JoinHostPort(parsed.Hostname(), ds.Port)
	}
	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host+parsed.Path, "/"), nil
}

func ambariGet(ds model.DataSource, path string, target any) error {
	base, err := ambariBaseURL(ds)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Requested-By", "OpsGuard")
	if strings.TrimSpace(ds.Username) != "" {
		req.SetBasicAuth(ds.Username, ds.Password)
	}
	response, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("Ambari 请求失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return errors.New("Ambari 认证失败，请检查账号、密码或 Kerberos/SSO 配置")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Ambari 返回 HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func ambariCluster(ds model.DataSource) (string, string, string, error) {
	var result struct {
		Items []struct {
			Clusters struct {
				Name    string `json:"cluster_name"`
				Version string `json:"version"`
				Stack   string `json:"stack_name"`
			} `json:"Clusters"`
		} `json:"items"`
	}
	if err := ambariGet(ds, "/api/v1/clusters", &result); err != nil {
		return "", "", "", err
	}
	if len(result.Items) == 0 || result.Items[0].Clusters.Name == "" {
		return "", "", "", errors.New("Ambari 未返回可访问的集群")
	}
	item := result.Items[0].Clusters
	return item.Name, item.Version, item.Stack, nil
}

func TestAmbariDataSource(ds model.DataSource) (string, error) {
	if strings.TrimSpace(ds.Username) == "" || strings.TrimSpace(ds.Password) == "" {
		return "", errors.New("Ambari 用户名和密码不能为空")
	}
	name, _, _, err := ambariCluster(ds)
	return name, err
}

func GetAmbariOverview(sourceID string) (model.AmbariOverview, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return model.AmbariOverview{}, err
	}
	if !strings.EqualFold(ds.Type, "ambari") {
		return model.AmbariOverview{}, errors.New("数据源不是 Ambari")
	}
	if err := FillDataSourcePassword(&ds); err != nil {
		return model.AmbariOverview{}, err
	}
	cluster, version, stack, err := ambariCluster(ds)
	if err != nil {
		return model.AmbariOverview{}, err
	}
	escaped := url.PathEscape(cluster)
	overview := model.AmbariOverview{ClusterName: cluster, Version: version, Stack: stack, Services: []model.AmbariService{}, Hosts: []model.AmbariHost{}, Alerts: []model.AmbariAlert{}, Requests: []model.AmbariRequest{}, Configs: map[string]string{}}
	var services struct {
		Items []struct {
			Service struct {
				Name        string `json:"service_name"`
				State       string `json:"state"`
				Maintenance string `json:"maintenance_state"`
			} `json:"ServiceInfo"`
		} `json:"items"`
	}
	if err := ambariGet(ds, "/api/v1/clusters/"+escaped+"/services?fields=ServiceInfo/service_name,ServiceInfo/state,ServiceInfo/maintenance_state", &services); err == nil {
		for _, item := range services.Items {
			overview.Services = append(overview.Services, model.AmbariService{Name: item.Service.Name, State: item.Service.State, Maintenance: item.Service.Maintenance})
		}
	}
	var hosts struct {
		Items []struct {
			Host struct {
				Name        string `json:"host_name"`
				Status      string `json:"host_status"`
				Maintenance string `json:"maintenance_state"`
			} `json:"Hosts"`
		} `json:"items"`
	}
	if err := ambariGet(ds, "/api/v1/clusters/"+escaped+"/hosts?fields=Hosts/host_name,Hosts/host_status,Hosts/maintenance_state", &hosts); err == nil {
		for _, item := range hosts.Items {
			overview.Hosts = append(overview.Hosts, model.AmbariHost{Name: item.Host.Name, Status: item.Host.Status, Maintenance: item.Host.Maintenance})
		}
	}
	var alerts struct {
		Items []struct {
			Alert struct {
				Label   string `json:"label"`
				State   string `json:"state"`
				Text    string `json:"text"`
				Host    string `json:"host_name"`
				Service string `json:"service_name"`
				Time    int64  `json:"last_time"`
			} `json:"Alert"`
		} `json:"items"`
	}
	if err := ambariGet(ds, "/api/v1/clusters/"+escaped+"/alerts?fields=Alert/label,Alert/state,Alert/text,Alert/host_name,Alert/service_name,Alert/last_time", &alerts); err == nil {
		for _, item := range alerts.Items {
			overview.Alerts = append(overview.Alerts, model.AmbariAlert{Label: item.Alert.Label, State: item.Alert.State, Text: item.Alert.Text, Host: item.Alert.Host, Service: item.Alert.Service, Time: item.Alert.Time})
		}
	}
	var requests struct {
		Items []struct {
			Request struct {
				ID      int64  `json:"id"`
				Context string `json:"request_context"`
				Status  string `json:"request_status"`
				Start   int64  `json:"start_time"`
				End     int64  `json:"end_time"`
			} `json:"Requests"`
		} `json:"items"`
	}
	if err := ambariGet(ds, "/api/v1/clusters/"+escaped+"/requests?fields=Requests/id,Requests/request_context,Requests/request_status,Requests/start_time,Requests/end_time&sortBy=Requests/id.desc&page_size=20", &requests); err == nil {
		for _, item := range requests.Items {
			overview.Requests = append(overview.Requests, model.AmbariRequest{ID: item.Request.ID, Context: item.Request.Context, Status: item.Request.Status, StartTime: item.Request.Start, EndTime: item.Request.End})
		}
	}
	return overview, nil
}
