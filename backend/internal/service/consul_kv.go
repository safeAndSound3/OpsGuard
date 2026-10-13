package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

type consulKVResponse struct {
	Key         string `json:"Key"`
	Value       string `json:"Value"`
	ModifyIndex uint64 `json:"ModifyIndex"`
}

func consulSource(sourceID string) (model.DataSource, string, error) {
	ds, err := GetDataSourceByID(strings.TrimSpace(sourceID))
	if err != nil || !strings.EqualFold(ds.Type, "consul") {
		return model.DataSource{}, "", errors.New("Consul 数据源不存在")
	}
	if !ds.Enabled {
		return model.DataSource{}, "", errors.New("Consul 数据源已停用")
	}
	if err := FillDataSourcePassword(&ds); err != nil {
		return model.DataSource{}, "", err
	}
	base := strings.TrimRight(strings.TrimSpace(ds.Host), "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" {
		return model.DataSource{}, "", errors.New("Consul 地址格式错误")
	}
	if parsed.Port() == "" && strings.TrimSpace(ds.Port) != "" {
		parsed.Host = net.JoinHostPort(parsed.Hostname(), strings.TrimSpace(ds.Port))
	}
	return ds, strings.TrimRight(parsed.String(), "/"), nil
}

func consulRequest(ds model.DataSource, method, endpoint string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ds.Username) != "" {
		req.SetBasicAuth(strings.TrimSpace(ds.Username), ds.Password)
	} else if strings.TrimSpace(ds.Password) != "" {
		req.Header.Set("X-Consul-Token", ds.Password)
	}
	return req, nil
}

// consulKVPath escapes each key segment while preserving Consul's hierarchical
// slash separators. Escaping the complete key would turn a/b into one segment.
func consulKVPath(key string) string {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(key), "/"), "/")
	encoded := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		encoded = append(encoded, url.PathEscape(part))
	}
	return strings.Join(encoded, "/")
}

func ListConsulKVKeys(sourceID, prefix string) ([]string, error) {
	ds, base, err := consulSource(sourceID)
	if err != nil {
		return nil, err
	}
	endpoint := base + "/v1/kv/" + consulKVPath(prefix) + "?keys"
	req, err := consulRequest(ds, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := safeHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("读取 Consul KV 列表失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return []string{}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("读取 Consul KV 列表失败：HTTP %d", resp.StatusCode)
	}
	keys := []string{}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&keys); err != nil {
		return nil, fmt.Errorf("解析 Consul KV 列表失败：%w", err)
	}
	return keys, nil
}

func GetConsulKVValue(sourceID, key string) (model.ConsulKVValue, error) {
	if strings.TrimSpace(key) == "" {
		return model.ConsulKVValue{}, errors.New("KV 键不能为空")
	}
	ds, base, err := consulSource(sourceID)
	if err != nil {
		return model.ConsulKVValue{}, err
	}
	req, err := consulRequest(ds, http.MethodGet, base+"/v1/kv/"+consulKVPath(key), nil)
	if err != nil {
		return model.ConsulKVValue{}, err
	}
	resp, err := safeHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return model.ConsulKVValue{}, fmt.Errorf("读取 Consul KV 值失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return model.ConsulKVValue{}, errors.New("KV 键不存在")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.ConsulKVValue{}, fmt.Errorf("读取 Consul KV 值失败：HTTP %d", resp.StatusCode)
	}
	var values []consulKVResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&values); err != nil || len(values) == 0 {
		return model.ConsulKVValue{}, errors.New("解析 Consul KV 值失败")
	}
	decoded, err := base64.StdEncoding.DecodeString(values[0].Value)
	if err != nil {
		return model.ConsulKVValue{}, errors.New("Consul KV 值不是有效 Base64 数据")
	}
	return model.ConsulKVValue{Key: values[0].Key, Value: string(decoded), ModifyIndex: values[0].ModifyIndex}, nil
}

func PutConsulKVValue(sourceID, key, value, format string, modifyIndex uint64, createOnly bool) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("KV 键不能为空")
	}
	ds, base, err := consulSource(sourceID)
	if err != nil {
		return err
	}
	endpoint := base + "/v1/kv/" + consulKVPath(key)
	if createOnly {
		endpoint += "?cas=0"
	} else if modifyIndex > 0 {
		endpoint += "?cas=" + strconv.FormatUint(modifyIndex, 10)
	}
	req, err := consulRequest(ds, http.MethodPut, endpoint, bytes.NewBufferString(value))
	if err != nil {
		return err
	}
	resp, err := safeHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("保存 Consul KV 失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("保存 Consul KV 失败：HTTP %d", resp.StatusCode)
	}
	var saved bool
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&saved); err == nil && !saved {
		return errors.New("KV 已被其他人修改，请重新读取后再保存")
	}
	return nil
}
