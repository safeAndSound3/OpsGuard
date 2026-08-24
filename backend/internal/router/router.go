package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"monitor-platform/internal/config"
	"monitor-platform/internal/model"
	"monitor-platform/internal/service"
)

func SetupRoutes(cfg config.AppConfig) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "env": cfg.Env})
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
			_ = json.NewDecoder(r.Body).Decode(&ds)
			if ds.ID == "" {
				ds.ID = fmt.Sprintf("ds-%d", time.Now().UnixNano())
			}
			ds.Status = "待测试"
			ds.LastTest = "未测试"
			added := service.AddDataSource(ds)
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
		_ = json.NewDecoder(r.Body).Decode(&ds)
		ok, msg := service.TestDataSourceConnection(ds)
		writeJSON(w, http.StatusOK, map[string]any{"success": ok, "message": msg, "latencyMs": 42})
	})

	// schema for a specific data source (mock)
	mux.HandleFunc("/api/data-sources/", func(w http.ResponseWriter, r *http.Request) {
		// expecting: /api/data-sources/{id}/schema
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		path := r.URL.Path
		// trim prefix
		p := strings.TrimPrefix(path, "/api/data-sources/")
		if strings.HasSuffix(p, "/schema") {
			id := strings.TrimSuffix(strings.TrimSuffix(p, "/schema"), "/")
			schema := service.GetSchemaForDataSource(id)
			writeJSON(w, http.StatusOK, map[string]any{"id": id, "schema": schema})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})

	mux.HandleFunc("/api/system-config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, service.GetSystemConfig())
	})

	mux.HandleFunc("/api/collection-rules", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"rules": service.ListCollectionRules()})
			return
		case http.MethodPost:
			var rule model.CollectionRule
			_ = json.NewDecoder(r.Body).Decode(&rule)
			added := service.AddCollectionRule(rule)
			writeJSON(w, http.StatusCreated, added)
			return
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
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
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
