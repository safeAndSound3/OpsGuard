package router

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"monitor-platform/internal/config"
	"monitor-platform/internal/model"
	"monitor-platform/internal/service"
	"monitor-platform/internal/webassets"
)

func SetupRoutes(cfg config.AppConfig) (*http.ServeMux, error) {
	if err := service.ConfigureOutboundPolicy(service.OutboundPolicy{AllowedHosts: cfg.OutboundAllowedHosts, AllowedCIDRs: cfg.OutboundAllowedCIDRs, AllowLoopback: cfg.AllowLoopbackTargets}); err != nil {
		return nil, err
	}
	if err := service.ConfigureSSHHostKeyPolicy(service.SSHHostKeyPolicy{Mode: cfg.SSHHostKeyMode, KnownHostsFile: cfg.SSHKnownHosts}); err != nil {
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
	mux.HandleFunc("/api/mysql-report-tasks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"items": service.ListMySQLReportTasks(r.URL.Query().Get("sourceId"))})
		case http.MethodPost, http.MethodPut:
			var task model.MySQLReportTask
			if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求内容格式无效"})
				return
			}
			saved, err := service.SaveMySQLReportTask(task)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, saved)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})
	mux.HandleFunc("/api/mysql-report-tasks/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/mysql-report-tasks/"), "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "报表任务不存在"})
			return
		}
		if parts[0] == "trend" && r.Method == http.MethodGet {
			points, err := service.ReportTrend(queryInt(r, "days", 7), r.URL.Query().Get("start"), r.URL.Query().Get("end"))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, points)
			return
		}
		id := parts[0]
		if len(parts) == 2 && parts[1] == "enabled" && r.Method == http.MethodPost {
			var request struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求内容格式无效"})
				return
			}
			task, err := service.SetMySQLReportTaskEnabled(id, request.Enabled)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, task)
			return
		}
		if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
			if err := service.RunReportNow(id); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]string{"message": "报表任务已开始执行"})
			return
		}
		if len(parts) == 2 && parts[1] == "retry" && r.Method == http.MethodPost {
			if err := service.RunReportNow(id); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]string{"message": "报表任务已重新提交执行"})
			return
		}
		if len(parts) == 2 && parts[1] == "result" && r.Method == http.MethodGet {
			result, err := service.LatestReportRun(id)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		if len(parts) == 2 && parts[1] == "runs" && r.Method == http.MethodGet {
			history, err := service.ListReportRuns(id, queryInt(r, "page", 1), queryInt(r, "pageSize", 10))
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "读取执行历史失败"})
				return
			}
			writeJSON(w, http.StatusOK, history)
			return
		}
		if len(parts) == 4 && parts[1] == "runs" && parts[3] == "result" && r.Method == http.MethodGet {
			result, err := service.ReportRunByID(id, parts[2])
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		if len(parts) == 5 && parts[1] == "runs" && parts[3] == "result" && parts[4] == "download" && r.Method == http.MethodGet {
			content, name, err := service.ReportRunCSV(id, parts[2])
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(name)))
			_, _ = w.Write(content)
			return
		}
		if len(parts) == 3 && parts[1] == "result" && parts[2] == "download" && r.Method == http.MethodGet {
			content, name, err := service.ReportResultCSV(id)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(name)))
			_, _ = w.Write(content)
			return
		}
		if r.Method == http.MethodDelete {
			if err := service.DeleteMySQLReportTask(id); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
			return
		}
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	})

	mux.HandleFunc("/api/knowledge/categories", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": service.ListKnowledgeCategories()})
	})
	mux.HandleFunc("/api/knowledge/docs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"items": service.ListKnowledgeDocs(r.URL.Query().Get("keyword"), r.URL.Query().Get("category"))})
		case http.MethodPost:
			var doc service.KnowledgeDoc
			if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求内容格式无效"})
				return
			}
			saved, err := service.SaveKnowledgeDoc(&doc)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, saved)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})
	mux.HandleFunc("/api/knowledge/docs/import", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件过大或格式无效"})
			return
		}
		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请选择要导入的 MD / ZIP 文件"})
			return
		}
		imports, err := readKnowledgeImports(files)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		created := []*service.KnowledgeDoc{}
		for _, item := range imports {
			doc, err := service.ImportKnowledgeDoc(item.name, item.content, "")
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "导入文档失败: " + err.Error()})
				return
			}
			created = append(created, doc)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": created, "count": len(created)})
	})
	mux.HandleFunc("/api/knowledge/docs/export-all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		docs := service.AllKnowledgeDocsForExport()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="knowledge-base.zip"`)
		zw := zip.NewWriter(w)
		seen := map[string]int{}
		for _, doc := range docs {
			name := strings.Map(func(r rune) rune {
				if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
					return '_'
				}
				return r
			}, doc.Title)
			if name == "" {
				name = doc.ID
			}
			base := name
			if n, ok := seen[base]; ok {
				seen[base] = n + 1
				name = fmt.Sprintf("%s-%d", base, n+1)
			} else {
				seen[base] = 1
			}
			f, err := zw.Create(name + ".md")
			if err != nil {
				continue
			}
			_, _ = f.Write([]byte(doc.Content))
		}
		_ = zw.Close()
	})
	mux.HandleFunc("/api/knowledge/docs/upload-image", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "图片过大"})
			return
		}
		file, h, err := r.FormFile("image")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请选择图片"})
			return
		}
		defer file.Close()
		ext := strings.ToLower(filepath.Ext(h.Filename))
		if ext == "" || len(ext) > 5 {
			ext = ".png"
		}
		allowed := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}
		if allowed[ext] == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "仅支持 png/jpg/gif/webp"})
			return
		}
		var header [512]byte
		n, err := io.ReadFull(file, header[:])
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取图片失败"})
			return
		}
		if http.DetectContentType(header[:n]) != allowed[ext] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "图片内容与格式不符"})
			return
		}
		id := fmt.Sprintf("%d", time.Now().UnixNano())
		name := "img-" + id + ext
		uploadDir := "uploads"
		_ = os.MkdirAll(uploadDir, 0755)
		dst, err := os.Create(filepath.Join(uploadDir, name))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存失败"})
			return
		}
		defer dst.Close()
		if _, err := dst.Write(header[:n]); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存失败"})
			return
		}
		if _, err := io.Copy(dst, file); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存失败"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"url": "/uploads/" + name})
	})
	mux.HandleFunc("/api/knowledge/docs/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/knowledge/docs/"), "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "文档不存在"})
			return
		}
		id := parts[0]
		if len(parts) == 2 && parts[1] == "export" && r.Method == http.MethodGet {
			doc, err := service.GetKnowledgeDoc(id)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			safeName := strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
					return r
				}
				return '_'
			}, doc.Title)
			if safeName == "" {
				safeName = "doc"
			}
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.md\"; filename*=UTF-8''%s.md", safeName, url.PathEscape(doc.Title)))
			_, _ = w.Write([]byte(doc.Content))
			return
		}
		switch r.Method {
		case http.MethodGet:
			doc, err := service.GetKnowledgeDoc(id)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, doc)
		case http.MethodPut:
			var doc service.KnowledgeDoc
			if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求内容格式无效"})
				return
			}
			doc.ID = id
			saved, err := service.SaveKnowledgeDoc(&doc)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, saved)
		case http.MethodDelete:
			if err := service.DeleteKnowledgeDoc(id); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
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
			service.RecordSecurityEvent("login_rate_limited", req.Username, requestClientIP(r), "too many failed attempts")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "登录失败次数过多，请 15 分钟后再试"})
			return
		}
		if !service.AuthenticateUser(req.Username, req.Password) {
			logins.failure(r, req.Username)
			service.RecordSecurityEvent("login_failed", req.Username, requestClientIP(r), "invalid credentials")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
			return
		}
		logins.success(r, req.Username)
		service.RecordSecurityEvent("login_succeeded", req.Username, requestClientIP(r), "")
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
		if strings.TrimSpace(req.NewPassword) == "" || len(req.NewPassword) < 8 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "新密码至少 8 位"})
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
	mux.HandleFunc("/api/hive-menu", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"items": service.ListHiveMenuItems()})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var item model.HiveMenuItem
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		saved, err := service.AddHiveMenuItem(item.SourceID, item.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	})
	mux.HandleFunc("/api/postgresql-menu", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"items": service.ListPostgreSQLMenuItems()})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var item model.PostgreSQLMenuItem
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		saved, err := service.AddPostgreSQLMenuItem(item.SourceID, item.Name)
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
		case http.MethodDelete:
			if err := service.DeleteRefreshSettings(r.URL.Query().Get("scope")); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, service.GetRefreshSettings(r.URL.Query().Get("scope")))
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
		started := time.Now()
		ok, msg := service.TestDataSourceConnection(ds)
		latencyMs := time.Since(started).Milliseconds()
		databases := []string{}
		if ok {
			if names, err := service.ListDataSourceDatabases(ds); err == nil {
				databases = names
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": ok, "message": msg, "latencyMs": latencyMs, "databases": databases})
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
		if len(parts) == 2 && parts[1] == "execute" && r.Method == http.MethodPost && strings.EqualFold(func() string { ds, _ := service.GetDataSourceByID(parts[0]); return ds.Type }(), "mysql") {
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
		if len(parts) == 3 && parts[1] == "agent-token" && parts[2] == "rotate" && r.Method == http.MethodPost {
			username, _ := sessions.username(r)
			token, err := service.RotateAgentToken(parts[0], username)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"token": token, "message": "Agent 令牌已轮换。请将新令牌追加到 Agent 的 TOKEN 配置（支持逗号分隔多个令牌）并重启 Agent，随后可移除旧令牌。"})
			return
		}
		if len(parts) == 3 && parts[1] == "agent-fingerprint" && parts[2] == "reset" && r.Method == http.MethodPost {
			if err := service.ResetAgentCertFingerprint(parts[0]); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
			return
		}
		if len(parts) == 3 && parts[1] == "ssh-hostkey" && parts[2] == "reset" && r.Method == http.MethodPost {
			if err := service.ResetSSHHostKeyFingerprint(parts[0]); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
			return
		}
		// expecting: /api/data-sources/{id}/schema
		if r.Method != http.MethodGet && !(len(parts) == 2 && parts[1] == "execute") {
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
		if len(parts) >= 2 && strings.EqualFold(func() string { ds, _ := service.GetDataSourceByID(parts[0]); return ds.Type }(), "hive") {
			switch parts[1] {
			case "databases":
				databases, err := service.ListHiveDatabases(parts[0])
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"databases": databases})
				return
			case "tables":
				tables, err := service.ListHiveTables(parts[0], r.URL.Query().Get("database"))
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
				return
			case "partitions":
				partitions, err := service.ListHivePartitions(parts[0], r.URL.Query().Get("database"), r.URL.Query().Get("table"))
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"partitions": partitions})
				return
			case "preview":
				preview, err := service.PreviewHiveTable(parts[0], r.URL.Query().Get("database"), r.URL.Query().Get("table"), r.URL.Query().Get("partition"), queryInt(r, "page", 1), queryInt(r, "pageSize", 50))
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, preview)
				return
			case "execute":
				if r.Method != http.MethodPost {
					writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
					return
				}
				var request struct {
					Database string `json:"database"`
					SQL      string `json:"sql"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
					return
				}
				result, err := service.ExecuteHiveStatement(parts[0], request.Database, request.SQL)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, result)
				return
			}
		}
		if len(parts) >= 2 {
			typeName := strings.ToLower(func() string { ds, _ := service.GetDataSourceByID(parts[0]); return ds.Type }())
			if typeName == "postgresql" || typeName == "postgres" {
				switch parts[1] {
				case "databases":
					databases, err := service.ListPostgreSQLDatabases(parts[0])
					if err != nil {
						writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
						return
					}
					writeJSON(w, http.StatusOK, map[string]any{"databases": databases})
					return
				case "tables":
					tables, err := service.ListPostgreSQLTables(parts[0], r.URL.Query().Get("database"))
					if err != nil {
						writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
						return
					}
					writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
					return
				case "preview":
					preview, err := service.PreviewPostgreSQLTable(parts[0], r.URL.Query().Get("database"), r.URL.Query().Get("table"), queryInt(r, "page", 1), queryInt(r, "pageSize", 50))
					if err != nil {
						writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
						return
					}
					writeJSON(w, http.StatusOK, preview)
					return
				case "execute":
					if r.Method != http.MethodPost {
						writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
						return
					}
					var request struct {
						Database string `json:"database"`
						SQL      string `json:"sql"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
						return
					}
					result, err := service.ExecutePostgreSQLStatement(parts[0], request.Database, request.SQL)
					if err != nil {
						writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
						return
					}
					writeJSON(w, http.StatusOK, result)
					return
				}
			}
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
		if len(parts) == 2 && parts[1] == "indexes" {
			indexes, err := service.ListMySQLTableIndexes(parts[0], r.URL.Query().Get("database"), r.URL.Query().Get("table"))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"indexes": indexes})
			return
		}
		if strings.HasSuffix(p, "/schema") {
			id := strings.TrimSuffix(strings.TrimSuffix(p, "/schema"), "/")
			schema, err := service.GetSchemaForDataSource(id, r.URL.Query().Get("database"), r.URL.Query().Get("table"), r.URL.Query().Get("full") == "1")
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
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
			writeJSON(w, http.StatusOK, map[string]int{"limit": service.GetMySQLQueryResultLimit(), "maxBytes": service.GetMySQLQueryResultMaxBytes(), "reportConcurrency": service.GetMySQLReportConcurrency()})
		case http.MethodPut:
			var request struct {
				Limit             int `json:"limit"`
				MaxBytes          int `json:"maxBytes"`
				ReportConcurrency int `json:"reportConcurrency"`
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
			if request.ReportConcurrency == 0 {
				request.ReportConcurrency = service.GetMySQLReportConcurrency()
			}
			if err := service.SaveMySQLReportConcurrency(request.ReportConcurrency); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]int{"limit": request.Limit, "maxBytes": request.MaxBytes, "reportConcurrency": request.ReportConcurrency})
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

	mux.HandleFunc("/api/ssh-custom-commands", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			items, err := service.ListSSRCustomCommands()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		case http.MethodPost:
			var body struct {
				ID       string `json:"id"`
				Label    string `json:"label"`
				Command  string `json:"command"`
				Category string `json:"category"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
				return
			}
			if strings.TrimSpace(body.Label) == "" || strings.TrimSpace(body.Command) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "名称和命令不能为空"})
				return
			}
			item, err := service.SaveSSRCustomCommand(body.ID, strings.TrimSpace(body.Label), strings.TrimSpace(body.Command), strings.TrimSpace(body.Category))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			id := strings.TrimPrefix(r.URL.Path, "/api/ssh-custom-commands/")
			if id == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少ID"})
				return
			}
			if err := service.DeleteSSRCustomCommand(id); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/api/ssh-exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var body struct {
			SourceID string `json:"sourceId"`
			Command  string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		body.SourceID = strings.TrimSpace(body.SourceID)
		body.Command = strings.TrimSpace(body.Command)
		if body.SourceID == "" || body.Command == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "节点和命令不能为空"})
			return
		}
		ds, err := service.GetDataSourceByID(body.SourceID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "数据源不存在"})
			return
		}
		if !strings.EqualFold(ds.Type, "SSH") && !strings.EqualFold(ds.Type, "Agent") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "该节点不是 SSH 类型"})
			return
		}
		out, exitCode, err := service.ExecuteSSHCommandPublic(ds, body.Command)
		service.RecordSSHCommandHistory(ds.ID, ds.Name, body.Command, exitCode)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"output": out, "exitCode": exitCode, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"output": out, "exitCode": exitCode})
	})

	mux.HandleFunc("/api/ssh-command-history", func(w http.ResponseWriter, r *http.Request) {
		sourceID := r.URL.Query().Get("sourceId")
		items, err := service.ListSSHCommandHistory(sourceID, 100)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
			items, err := service.ListPrometheusMetricNames(parts[0], 100000)
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
		writeJSON(w, http.StatusNotImplemented, map[string]any{"success": false, "error": "尚未配置告警消息发送通道，未发送测试消息"})
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
	root.Handle("/uploads/", http.StripPrefix("/uploads/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentTypes := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}
		contentType := contentTypes[strings.ToLower(filepath.Ext(r.URL.Path))]
		if contentType == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.FileServer(http.Dir("uploads")).ServeHTTP(w, r)
	})))
	root.Handle("/", webassets.Handler())
	return wrapAsServeMux(corsMiddleware(limitRequestBody(root), cfg.CORSAllowedOrigins)), nil
}

type knowledgeImport struct{ name, content string }

func readKnowledgeImports(files []*multipart.FileHeader) ([]knowledgeImport, error) {
	const maxArchiveBytes = 20 << 20
	const maxDocumentBytes = 8 << 20
	const maxExpandedBytes = 32 << 20
	const maxEntries = 200
	items := make([]knowledgeImport, 0)
	total := int64(0)
	entries := 0
	add := func(name string, r io.Reader) error {
		if entries >= maxEntries {
			return fmt.Errorf("导入文件数量超过 %d 个", maxEntries)
		}
		limit := int64(maxDocumentBytes)
		if remaining := int64(maxExpandedBytes) - total; remaining < limit {
			limit = remaining
		}
		content, err := io.ReadAll(io.LimitReader(r, limit+1))
		if err != nil {
			return fmt.Errorf("读取文档 %s 失败: %w", name, err)
		}
		if int64(len(content)) > limit {
			return fmt.Errorf("文档 %s 超过导入大小限制", name)
		}
		total += int64(len(content))
		entries++
		items = append(items, knowledgeImport{name: filepath.Base(strings.ReplaceAll(name, "\\", "/")), content: string(content)})
		return nil
	}
	for _, file := range files {
		if file.Size > maxArchiveBytes {
			return nil, fmt.Errorf("文件 %s 超过 20 MiB", file.Filename)
		}
		body, err := file.Open()
		if err != nil {
			return nil, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(body, maxArchiveBytes+1))
		_ = body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(raw) > maxArchiveBytes {
			return nil, fmt.Errorf("文件 %s 超过 20 MiB", file.Filename)
		}
		lower := strings.ToLower(file.Filename)
		if strings.HasSuffix(lower, ".zip") {
			archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				return nil, fmt.Errorf("无效 ZIP 文件 %s: %w", file.Filename, err)
			}
			if len(archive.File) > maxEntries {
				return nil, fmt.Errorf("ZIP 条目数量超过 %d 个", maxEntries)
			}
			for _, entry := range archive.File {
				name := strings.ToLower(entry.Name)
				if entry.FileInfo().IsDir() || (!strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".markdown")) {
					continue
				}
				r, err := entry.Open()
				if err != nil {
					return nil, err
				}
				err = add(entry.Name, r)
				_ = r.Close()
				if err != nil {
					return nil, err
				}
			}
		} else if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") {
			if err := add(file.Filename, bytes.NewReader(raw)); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("不支持文件 %s", file.Filename)
		}
	}
	return items, nil
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
			service.RecordOperation(username, action, details, requestClientIP(r))
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
	if strings.Contains(path, "/agent-token/rotate") {
		return "轮换 Agent 令牌"
	}
	if strings.Contains(path, "/agent-fingerprint/reset") {
		return "重置 Agent 证书指纹"
	}
	if strings.Contains(path, "/ssh-hostkey/reset") {
		return "重置 SSH 主机密钥指纹"
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

// requestClientIP stores only the peer IP, never its ephemeral TCP port.
// OpsGuard is deployed directly and deliberately does not trust unverified
// forwarding headers such as X-Forwarded-For.
func requestClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return strings.Trim(remoteAddr, "[]")
}
