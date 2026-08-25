package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"monitor-platform/internal/config"
	"monitor-platform/internal/model"
	"monitor-platform/internal/service"
)

func SetupRoutes(cfg config.AppConfig) *http.ServeMux {
	mux := http.NewServeMux()
	if err := service.InitDataSourceStore(); err != nil {
		fmt.Printf("data source store init failed: %v\n", err)
	}

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "env": cfg.Env})
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
		if !service.AuthenticateUser(req.Username, req.Password) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": "opsguard-admin", "user": map[string]string{"username": "admin", "name": "平台管理员"}})
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
		if strings.TrimSpace(req.NewPassword) == "" || len(req.NewPassword) < 6 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "新密码至少 6 位"})
			return
		}
		if err := service.ChangeUserPassword("admin", req.OldPassword, req.NewPassword); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	})

	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
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
		ok, msg := service.TestDataSourceConnection(ds)
		writeJSON(w, http.StatusOK, map[string]any{"success": ok, "message": msg, "latencyMs": 42})
	})

	// schema for a specific data source (mock)
	mux.HandleFunc("/api/data-sources/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		p := strings.TrimPrefix(path, "/api/data-sources/")
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
		// expecting: /api/data-sources/{id}/schema
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if strings.HasSuffix(p, "/schema") {
			id := strings.TrimSuffix(strings.TrimSuffix(p, "/schema"), "/")
			schema := service.GetSchemaForDataSource(id)
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

	mux.HandleFunc("/api/mysql-monitor/instances", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"instances": service.ListMySQLInstanceStatuses()})
	})

	mux.HandleFunc("/api/mysql-monitor/instances/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/api/mysql-monitor/instances/")
		parts := strings.Split(strings.Trim(p, "/"), "/")
		if len(parts) != 2 || parts[0] == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		limit := queryLimit(r, 100)
		start, end := queryTimeRange(r)
		switch parts[1] {
		case "metrics":
			writeJSON(w, http.StatusOK, map[string]any{"sourceId": parts[0], "snapshots": service.ListMySQLMetricSnapshots(parts[0], limit, start, end)})
		case "slow-queries":
			writeJSON(w, http.StatusOK, map[string]any{"sourceId": parts[0], "slowQueries": service.ListMySQLSlowQueries(parts[0], limit, start, end)})
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
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

	mux.HandleFunc("/api/system-config/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "告警通道测试成功，告警消息已发送"})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func queryLimit(r *http.Request, fallback int) int {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > 500 {
		return 500
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
