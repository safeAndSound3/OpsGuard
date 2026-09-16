package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"monitor-platform/internal/config"
	"monitor-platform/internal/model"
	"monitor-platform/internal/service"
)

func SetupRoutes(cfg config.AppConfig) (*http.ServeMux, error) {
	if err := service.ConfigureOutboundPolicy(service.OutboundPolicy{AllowedHosts: cfg.OutboundAllowedHosts, AllowedCIDRs: cfg.OutboundAllowedCIDRs, AllowLoopback: cfg.AllowLoopbackTargets}); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	sessions := newSessionManager(cfg)
	logins := newLoginLimiter()
	if err := service.InitDataSourceStore(); err != nil {
		return nil, fmt.Errorf("initialize MySQL store: %w", err)
	}

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "env": cfg.Env})
	})
	mux.HandleFunc("/api/mysql-jobs/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/mysql-jobs/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
			return
		}
		id := parts[0]
		if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
			if err := service.CancelMySQLAsyncJob(id); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
			return
		}
		if len(parts) == 2 && parts[1] == "download" && r.Method == http.MethodGet {
			content, name, err := service.ReadMySQLAsyncJobFile(id)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
			_, _ = w.Write(content)
			return
		}
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		job, err := service.GetMySQLAsyncJob(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
			return
		}
		writeJSON(w, http.StatusOK, job)
	})
	mux.HandleFunc("/api/mysql-jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": service.ListMySQLAsyncJobs(r.URL.Query().Get("sourceId"))})
	})
	mux.HandleFunc("/api/audit-logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, service.ListAuditLogs(service.AuditLogQuery{
			Action: r.URL.Query().Get("action"), Username: r.URL.Query().Get("username"), Keyword: r.URL.Query().Get("keyword"),
			Page: queryInt(r, "page", 1), PageSize: queryInt(r, "pageSize", 20),
		}))
	})

	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if !logins.allowed(r, req.Username) {
			service.RecordSecurityEvent("login_rate_limited", req.Username, r.RemoteAddr, "too many failed attempts")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "登录失败次数过多，请 15 分钟后再试"})
			return
		}
		if !service.AuthenticateUser(req.Username, req.Password) {
			logins.failure(r, req.Username)
			service.RecordSecurityEvent("login_failed", req.Username, r.RemoteAddr, "invalid credentials")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
			return
		}
		logins.success(r, req.Username)
		service.RecordSecurityEvent("login_succeeded", req.Username, r.RemoteAddr, "")
		if _, err := sessions.create(w, req.Username); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "创建登录会话失败"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": map[string]string{"username": req.Username, "name": "平台管理员"}})
	})

	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		username, _ := sessions.username(r)
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": map[string]string{"username": username, "name": "平台管理员"}})
	})

	mux.HandleFunc("/api/change-password", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			OldPassword string `json:"oldPassword"`
			NewPassword string `json:"newPassword"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if strings.TrimSpace(req.NewPassword) == "" || len(req.NewPassword) < 12 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "新密码至少 12 位"})
			return
		}
		username, ok := sessions.username(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "登录会话无效"})
			return
		}
		if err := service.ChangeUserPassword(username, req.OldPassword, req.NewPassword); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		sessions.destroy(w, r)
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	})

	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		sessions.destroy(w, r)
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	})

	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"metrics": service.GetOverviewMetrics(),
			"alerts":  service.GetAlerts(),
		})
	})

	mux.HandleFunc("/api/inspection", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"tasks": service.GetInspectionTasks()})
	})

	mux.HandleFunc("/api/platform-links", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"links": service.ListPlatformLinks()})
		case http.MethodPut:
			var request struct {
				Links []model.PlatformLink `json:"links"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			links, err := service.ReplacePlatformLinks(request.Links)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"links": links})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/api/hadoop-menu", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"items": service.ListHadoopMenuItems()})
		case http.MethodPost:
			var request model.HadoopMenuItem
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			item, err := service.AddHadoopMenuItem(request.SourceID, request.Name)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/api/hadoop-menu/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		sourceID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hadoop-menu/"), "/")
		if sourceID == "" || strings.Contains(sourceID, "/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if err := service.DeleteHadoopMenuItem(sourceID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	})

	mux.HandleFunc("/api/ambari-menu", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"items": service.ListAmbariMenuItems()})
		case http.MethodPost:
			var item model.AmbariMenuItem
			if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			saved, err := service.AddAmbariMenuItem(item.SourceID, item.Name)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusCreated, saved)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})
	mux.HandleFunc("/api/ambari-menu/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/ambari-menu/"), "/")
		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if err := service.DeleteAmbariMenuItem(id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	})
	mux.HandleFunc("/api/mysql-menu", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"items": service.ListMySQLMenuItems()})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var item model.MySQLMenuItem
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		saved, err := service.AddMySQLMenuItem(item.SourceID, item.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	})

	mux.HandleFunc("/api/consul-menu", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"items": service.ListConsulMenuItems()})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var item model.ConsulMenuItem
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		saved, err := service.AddConsulMenuItem(item.SourceID, item.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	})

	mux.HandleFunc("/api/consul/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/consul/"), "/"), "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] != "kv" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		sourceID := parts[0]
		switch r.Method {
		case http.MethodGet:
			key := strings.TrimSpace(r.URL.Query().Get("key"))
			if key == "" {
				keys, err := service.ListConsulKVKeys(sourceID, r.URL.Query().Get("prefix"))
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
				return
			}
			value, err := service.GetConsulKVValue(sourceID, key)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, value)
		case http.MethodPut:
			var value model.ConsulKVValue
			if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&value); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			if err := service.PutConsulKVValue(sourceID, value.Key, value.Value, value.Format, value.ModifyIndex, value.Create); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/api/dashboards", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"items": service.ListDashboardItems()})
		case http.MethodPost:
			var item model.DashboardItem
			if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			added, err := service.AddDashboardItem(item)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusCreated, added)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/api/dashboards/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/dashboards/"), "/")
		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if err := service.DeleteDashboardItem(id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	})

	mux.HandleFunc("/api/settings/refresh", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, service.GetRefreshSettings(r.URL.Query().Get("scope")))
		case http.MethodPut:
			var settings model.RefreshSettings
			if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			saved, err := service.SaveRefreshSettings(settings, r.URL.Query().Get("scope"))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, saved)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})
	mux.HandleFunc("/api/platform-settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]string{"platformName": service.GetPlatformName()})
		case http.MethodPut:
			var request struct {
				PlatformName string `json:"platformName"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
				return
			}
			name, err := service.SavePlatformName(request.PlatformName)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"platformName": name})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	// list and add data sources
	mux.HandleFunc("/api/data-sources", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"dataSources": service.ListDataSources()})
			return
		case http.MethodPost:
			var ds model.DataSource
			if err := json.NewDecoder(r.Body).Decode(&ds); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			if ds.ID == "" {
				ds.ID = fmt.Sprintf("ds-%d", time.Now().UnixNano())
			}
			added, err := service.AddDataSource(ds)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusCreated, added)
			return
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
	})

	mux.HandleFunc("/api/data-sources/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var ds model.DataSource
		if err := json.NewDecoder(r.Body).Decode(&ds); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if ds.Password == "" && ds.ID != "" {
			_ = service.FillDataSourcePassword(&ds)
		}
		ok, msg := service.TestDataSourceConnection(ds)
		databases := []string{}
		if ok {
			if names, err := service.ListDataSourceDatabases(ds); err == nil {
				databases = names
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": ok, "message": msg, "latencyMs": 42, "databases": databases})
	})

	mux.HandleFunc("/api/data-sources/health-check", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"dataSources": service.RefreshDataSourceHealth()})
	})

	// schema for a specific data source (mock)
	mux.HandleFunc("/api/data-sources/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		p := strings.TrimPrefix(path, "/api/data-sources/")
		parts := strings.Split(strings.Trim(p, "/"), "/")
		if r.Method == http.MethodPut && len(parts) == 2 && parts[1] == "enabled" {
			id := parts[0]
			if id == "" {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			var req struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			updated, err := service.SetDataSourceEnabled(id, req.Enabled)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, updated)
			return
		}
		if len(parts) == 2 && parts[1] == "rows" {
			var request struct {
				Database string         `json:"database"`
				Table    string         `json:"table"`
				Keys     map[string]any `json:"keys"`
				Values   map[string]any `json:"values"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			var result service.MySQLMutationResult
			var err error
			switch r.Method {
			case http.MethodPost:
				result, err = service.InsertMySQLTableRow(parts[0], request.Database, request.Table, request.Values)
			case http.MethodPut:
				result, err = service.UpdateMySQLTableRow(parts[0], request.Database, request.Table, request.Keys, request.Values)
			case http.MethodDelete:
				result, err = service.DeleteMySQLTableRow(parts[0], request.Database, request.Table, request.Keys)
			default:
				writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
				return
			}
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		if r.Method == http.MethodPut {
			id := strings.Trim(p, "/")
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			var ds model.DataSource
			if err := json.NewDecoder(r.Body).Decode(&ds); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			updated, err := service.UpdateDataSource(id, ds)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, updated)
			return
		}
		if r.Method == http.MethodDelete {
			id := strings.Trim(p, "/")
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			if err := service.DeleteDataSource(id); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
			return
		}
		if len(parts) == 2 && parts[1] == "export" && r.Method == http.MethodGet {
			content, filename, err := service.ExportMySQLData(parts[0], service.MySQLTransferOptions{Database: r.URL.Query().Get("database"), Table: r.URL.Query().Get("table"), Scope: r.URL.Query().Get("scope"), Format: r.URL.Query().Get("format")})
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			contentType := "application/octet-stream"
			switch strings.ToLower(r.URL.Query().Get("format")) {
			case "sql":
				contentType = "application/sql; charset=utf-8"
			case "csv":
				contentType = "text/csv; charset=utf-8"
			case "json":
				contentType = "application/json; charset=utf-8"
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
			return
		}
		if len(parts) == 2 && parts[1] == "export-jobs" && r.Method == http.MethodPost {
			var request struct {
				Database string `json:"database"`
				Table    string `json:"table"`
				Scope    string `json:"scope"`
				Format   string `json:"format"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
				return
			}
			job, err := service.StartMySQLExportJob(parts[0], service.MySQLTransferOptions{Database: request.Database, Table: request.Table, Scope: request.Scope, Format: request.Format})
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusAccepted, job)
			return
		}
		if len(parts) == 2 && parts[1] == "import-jobs" && r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 20*1024*1024)
			if err := r.ParseMultipartForm(20 * 1024 * 1024); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "导入文件必须在 20 MB 以内"})
				return
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请选择导入文件"})
				return
			}
			defer file.Close()
			content, err := io.ReadAll(file)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取导入文件失败"})
				return
			}
			upload, err := service.SaveMySQLJobUpload(header.Filename, content)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			confirmed, _ := strconv.ParseBool(r.FormValue("confirmed"))
			job, err := service.StartMySQLImportJob(parts[0], r.FormValue("database"), r.FormValue("table"), r.FormValue("format"), upload, header.Filename, confirmed)
			if err != nil {
				_ = os.Remove(upload)
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusAccepted, job)
			return
		}
		if len(parts) == 2 && parts[1] == "query-jobs" && r.Method == http.MethodPost {
			var request struct {
				Database  string `json:"database"`
				SQL       string `json:"sql"`
				Confirmed bool   `json:"confirmed"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
				return
			}
			username, _ := sessions.username(r)
			job, err := service.StartMySQLQueryJob(parts[0], request.Database, request.SQL, username, request.Confirmed)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusAccepted, job)
			return
		}
		if len(parts) == 2 && parts[1] == "execute" && r.Method == http.MethodPost {
			var request struct {
				Database  string `json:"database"`
				SQL       string `json:"sql"`
				Confirmed bool   `json:"confirmed"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			username, _ := sessions.username(r)
			result, err := service.ExecuteMySQLStatement(parts[0], request.Database, request.SQL, username, request.Confirmed)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		if len(parts) == 2 && parts[1] == "sql-history" && r.Method == http.MethodGet {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
			items, total := service.ListMySQLSQLHistory(parts[0], page, pageSize)
			if page < 1 {
				page = 1
			}
			if pageSize < 1 || pageSize > 100 {
				pageSize = 10
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "pageSize": pageSize})
			return
		}
		if len(parts) == 2 && parts[1] == "agent-status" && r.Method == http.MethodGet {
			status, err := service.GetAgentStatus(parts[0])
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, status)
			return
		}
		// expecting: /api/data-sources/{id}/schema
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if len(parts) == 2 && parts[1] == "dashboard-metrics" {
			ds, err := service.GetDataSourceByID(parts[0])
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "data source not found"})
				return
			}
			var metrics map[string]float64
			var collectedAt time.Time
			if strings.EqualFold(ds.Type, "mysql") {
				metrics, collectedAt, err = service.LatestMySQLDashboardMetrics(ds.ID)
			} else if strings.EqualFold(ds.Type, "ssh") || strings.EqualFold(ds.Type, "agent") {
				metrics, collectedAt, err = service.LatestSSHDashboardMetrics(ds.ID)
			} else if strings.EqualFold(ds.Type, "redis") || strings.EqualFold(ds.Type, "clickhouse") || strings.EqualFold(ds.Type, "kafka") {
				metrics, collectedAt, err = service.LatestMiddlewareDashboardMetrics(ds.ID)
			} else {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported dashboard data source"})
				return
			}
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{"metrics": map[string]float64{}, "collectedAt": "", "message": "暂无采集样本"})
				return
			}
			response := map[string]any{"metrics": metrics, "collectedAt": collectedAt}
			if strings.EqualFold(ds.Type, "ssh") || strings.EqualFold(ds.Type, "agent") {
				filesystems, _, filesystemErr := service.LatestHostFilesystemMetrics(ds.ID)
				if filesystemErr == nil {
					response["filesystems"] = filesystems
				}
			}
			writeJSON(w, http.StatusOK, response)
			return
		}
		if len(parts) == 2 && parts[1] == "hadoop-health" {
			if !strings.EqualFold(func() string { ds, _ := service.GetDataSourceByID(parts[0]); return ds.Type }(), "hadoop") {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data source is not Hadoop"})
				return
			}
			health, err := service.GetHadoopHealth(parts[0])
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": health})
			return
		}
		if len(parts) == 2 && parts[1] == "dashboard-sql" {
			items, mode, err := service.ListMySQLDashboardSQL(parts[0])
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items, "mode": mode})
			return
		}
		if len(parts) == 2 && parts[1] == "preview" {
			preview, err := service.PreviewMySQLTableWithOptions(parts[0], r.URL.Query().Get("database"), r.URL.Query().Get("table"), queryInt(r, "page", 1), queryInt(r, "pageSize", 50), service.MySQLTablePreviewOptions{
				FilterColumn:  r.URL.Query().Get("filterColumn"),
				FilterValue:   r.URL.Query().Get("filterValue"),
				SortColumn:    r.URL.Query().Get("sortColumn"),
				SortDirection: r.URL.Query().Get("sortDirection"),
			})
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, preview)
			return
		}
		if len(parts) == 2 && parts[1] == "structure" {
			columns, err := service.DescribeMySQLTable(parts[0], r.URL.Query().Get("database"), r.URL.Query().Get("table"))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"columns": columns})
			return
		}
		if strings.HasSuffix(p, "/schema") {
			id := strings.TrimSuffix(strings.TrimSuffix(p, "/schema"), "/")
			schema := service.GetSchemaForDataSource(id, r.URL.Query().Get("database"), r.URL.Query().Get("table"))
			databases := make([]string, 0, len(schema))
			for databaseName := range schema {
				databases = append(databases, databaseName)
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": id, "databases": databases, "schema": schema})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})

	mux.HandleFunc("/api/system-config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, service.GetSystemConfig())
	})
	mux.HandleFunc("/api/system-settings/confirmations", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]bool{"requireWriteConfirmation": service.GetOperationConfirmations()})
		case http.MethodPut:
			var request struct {
				RequireWriteConfirmation *bool `json:"requireWriteConfirmation"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.RequireWriteConfirmation == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
				return
			}
			if err := service.SaveOperationConfirmations(*request.RequireWriteConfirmation); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存操作确认设置失败"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"requireWriteConfirmation": *request.RequireWriteConfirmation})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})
	mux.HandleFunc("/api/system-settings/mysql-query-limit", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]int{"limit": service.GetMySQLQueryResultLimit(), "maxBytes": service.GetMySQLQueryResultMaxBytes()})
		case http.MethodPut:
			var request struct {
				Limit    int `json:"limit"`
				MaxBytes int `json:"maxBytes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
				return
			}
			if err := service.SaveMySQLQueryResultLimit(request.Limit); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if err := service.SaveMySQLQueryResultMaxBytes(request.MaxBytes); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]int{"limit": request.Limit, "maxBytes": request.MaxBytes})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/api/exporters/mysql", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(service.ExportMySQLPrometheusMetrics()))
	})

	mux.HandleFunc("/api/prometheus/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/api/prometheus/")
		parts := strings.Split(strings.Trim(p, "/"), "/")
		if len(parts) != 2 || parts[0] == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		switch parts[1] {
		case "metrics":
			items, err := service.ListPrometheusMetricNames(parts[0], queryLimitMax(r, 1000, 5000))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"metrics": items})
		case "query":
			data, err := service.QueryPrometheusDataSourceByID(parts[0], r.URL.Query().Get("query"))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": data})
		case "alerts":
			items, err := service.ListPrometheusDataSourceAlerts(parts[0])
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"alerts": items})
		case "rules":
			items, err := service.ListPrometheusDataSourceRules(parts[0])
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"rules": items})
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
	})

	mux.HandleFunc("/api/hadoop/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hadoop/"), "/"), "/")
		if len(parts) < 2 || parts[0] == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		var payload any
		var err error
		switch parts[1] {
		case "apps":
			if len(parts) == 2 {
				payload, err = service.ListHadoopApplications(parts[0], service.HadoopApplicationQuery{
					Page: queryInt(r, "page", 1), PageSize: queryInt(r, "pageSize", 20), Keyword: r.URL.Query().Get("keyword"),
					User: r.URL.Query().Get("user"), Type: r.URL.Query().Get("type"), State: r.URL.Query().Get("state"), FinalStatus: r.URL.Query().Get("finalStatus"),
				})
			} else if len(parts) == 3 {
				payload, err = service.ListHadoopContainers(parts[0], parts[2])
			} else {
				err = fmt.Errorf("not found")
			}
		case "log":
			payload, err = service.HadoopContainerLog(parts[0], r.URL.Query().Get("url"))
		default:
			err = fmt.Errorf("not found")
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": payload})
	})

	mux.HandleFunc("/api/ambari/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/ambari/"), "/"), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] != "overview" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		data, err := service.GetAmbariOverview(parts[0])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	})

	mux.HandleFunc("/api/collection-rules", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"rules": service.ListCollectionRules()})
			return
		case http.MethodPost:
			var rule model.CollectionRule
			if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			added, err := service.AddCollectionRule(rule)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusCreated, added)
			return
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
	})

	mux.HandleFunc("/api/collection-rules/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/collection-rules/"), "/")
		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		switch r.Method {
		case http.MethodPut:
			var rule model.CollectionRule
			if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			updated, err := service.UpdateCollectionRule(id, rule)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, updated)
		case http.MethodDelete:
			if err := service.DeleteCollectionRule(id); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/api/notifications", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		items := service.ListAlertNotifications(r.URL.Query().Get("status"), r.URL.Query().Get("unread"), r.URL.Query().Get("start"), r.URL.Query().Get("end"), queryLimit(r, 100))
		writeJSON(w, http.StatusOK, map[string]any{"notifications": items, "unread": service.AlertNotificationUnreadCount()})
	})

	mux.HandleFunc("/api/notifications/read", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := service.MarkAlertNotificationsRead(req.ID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "unread": service.AlertNotificationUnreadCount()})
	})

	mux.HandleFunc("/api/notifications/mute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			ID    string `json:"id"`
			Muted bool   `json:"muted"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求参数错误"})
			return
		}
		if err := service.SetAlertNotificationMuted(req.ID, req.Muted); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "unread": service.AlertNotificationUnreadCount()})
	})

	mux.HandleFunc("/api/system-config/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "告警通道测试成功，告警消息已发送"})
	})

	root := http.NewServeMux()
	protected := operationAuditMiddleware(sessions.require(mux), sessions)
	root.Handle("/health", mux)
	root.Handle("/api/login", mux)
	root.Handle("/api/platform-settings", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			protected.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"platformName": service.GetPlatformName()})
	}))
	root.Handle("/api/", protected)
	return wrapAsServeMux(corsMiddleware(limitRequestBody(root), cfg.CORSAllowedOrigins)), nil
}

func wrapAsServeMux(handler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	if values, ok := payload.(map[string]string); ok {
		if message, exists := values["error"]; exists {
			values["error"] = localizeAPIError(sanitizeAPIError(message))
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// Do not expose driver, filesystem, network, or remote-service internals to
// browsers. Detailed errors remain available in server logs.
func sanitizeAPIError(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, marker := range []string{"dial tcp", "connectex", "connection refused", "sql:", "error 114", "agent 返回", "read ", "open ", "lookup ", "known_hosts", "panic"} {
		if strings.Contains(lower, marker) {
			return "请求处理失败，请检查配置或服务状态"
		}
	}
	if len(value) > 300 {
		return value[:300] + "..."
	}
	return value
}

func localizeAPIError(value string) string {
	for english, chinese := range map[string]string{"method not allowed": "不支持该请求方法", "invalid request body": "请求内容格式无效", "not found": "请求对象不存在", "data source not found": "数据源不存在", "unsupported dashboard data source": "该数据源不支持监控大屏", "data source is not Hadoop": "该数据源不是 Hadoop"} {
		if value == english {
			return chinese
		}
	}
	return value
}

func operationAuditMiddleware(next http.Handler, sessions *sessionManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Body != nil && !strings.Contains(r.Header.Get("Content-Type"), "multipart/") {
			body, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		username, authenticated := sessions.username(r)
		recorder := &auditStatusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.URL.Path == "/api/audit-logs" {
			return
		}
		if authenticated {
			details := auditResourceLabel(r.Method, r.URL.Path, body)
			action := "操作审计"
			if recorder.status < http.StatusOK || recorder.status >= http.StatusMultipleChoices {
				action = "操作失败"
				details += fmt.Sprintf("：请求失败（%d）", recorder.status)
			}
			service.RecordOperation(username, action, details, r.RemoteAddr)
		}
	})
}

type auditStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *auditStatusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *auditStatusRecorder) Write(body []byte) (int, error) { return w.ResponseWriter.Write(body) }

func auditResourceLabel(method, path string, body []byte) string {
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	name := strings.TrimSpace(fmt.Sprint(payload["name"]))
	if name == "<nil>" {
		name = ""
	}
	withName := func(action string) string {
		if name != "" {
			return action + "【" + name + "】"
		}
		return action
	}
	if strings.HasSuffix(path, "/import") {
		return "导入数据库文件"
	}
	if strings.HasSuffix(path, "/export") {
		return "导出数据库文件"
	}
	if strings.HasSuffix(path, "/execute") {
		return "执行 SQL"
	}
	if strings.Contains(path, "/data-sources") {
		if method == http.MethodPost {
			return withName("新增数据源")
		}
		if method == http.MethodDelete {
			return "删除数据源【" + path[strings.LastIndex(path, "/")+1:] + "】"
		}
		return withName("修改数据源")
	}
	if strings.Contains(path, "/consul/") && method == http.MethodPut {
		key := strings.TrimSpace(fmt.Sprint(payload["key"]))
		if key != "" && key != "<nil>" {
			return "保存 Consul KV【" + key + "】"
		}
		return "保存 Consul KV"
	}
	if strings.Contains(path, "/alerts") || strings.Contains(path, "/collection-rules") {
		if method == http.MethodPost {
			return withName("新增告警规则")
		}
		if method == http.MethodDelete {
			return "删除告警规则"
		}
		return withName("修改告警规则")
	}
	if method == http.MethodDelete {
		return "删除平台对象"
	}
	if method == http.MethodPut {
		return "修改平台配置"
	}
	return "执行平台操作"
}

func queryLimit(r *http.Request, fallback int) int {
	return queryLimitMax(r, fallback, 500)
}

func queryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(key)))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func queryLimitMax(r *http.Request, fallback int, max int) int {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > max {
		return max
	}
	return limit
}

func queryTimeRange(r *http.Request) (*time.Time, *time.Time) {
	return parseQueryTime(r.URL.Query().Get("start")), parseQueryTime(r.URL.Query().Get("end"))
}

func parseQueryTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	layouts := []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return &parsed
		}
	}
	return nil
}
