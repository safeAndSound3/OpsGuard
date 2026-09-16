package service

import (
	"context"
	"database/sql"
	"time"
)

func initSessionStore(current *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS user_sessions (
		token_hash char(64) PRIMARY KEY,
		username varchar(64) NOT NULL,
		expires_at datetime NOT NULL,
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_user_sessions_expires_at (expires_at)
	)`); err != nil {
		return err
	}
	_, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS audit_logs (
		id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
		username varchar(64) NOT NULL DEFAULT '', action varchar(128) NOT NULL,
		details varchar(512) NOT NULL DEFAULT '', remote_addr varchar(255) NOT NULL DEFAULT '',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_audit_logs_created_at (created_at)
	)`)
	if err != nil {
		return err
	}
	// Migrate the former operation table before removing both legacy audit tables.
	_, _ = current.ExecContext(ctx, `INSERT INTO audit_logs (username, action, details, remote_addr, created_at)
		SELECT username, action, resource, remote_addr, created_at FROM operation_audit_log`)
	_, _ = current.ExecContext(ctx, `DROP TABLE IF EXISTS security_audit_log`)
	_, _ = current.ExecContext(ctx, `DROP TABLE IF EXISTS operation_audit_log`)
	// Agent command audit is intentionally disabled; remove legacy entries so
	// the unified log center does not display historical command records.
	_, _ = current.ExecContext(ctx, `DELETE FROM audit_logs WHERE action = 'Agent 命令审计'`)
	_, _ = current.ExecContext(ctx, `UPDATE audit_logs SET action = CASE action
		WHEN 'login_succeeded' THEN '登录成功' WHEN 'login_failed' THEN '登录失败'
		WHEN 'login_rate_limited' THEN '登录受限' ELSE action END,
		details = CASE details WHEN 'invalid credentials' THEN '账号或密码错误' WHEN 'too many failed attempts' THEN '登录失败次数过多' ELSE details END`)
	return err
}

func StoreSession(tokenHash, username string, expiresAt time.Time) error {
	current := currentStore()
	if current == nil {
		return nil
	}
	_, err := current.Exec(`INSERT INTO user_sessions (token_hash, username, expires_at) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE username=VALUES(username), expires_at=VALUES(expires_at)`, tokenHash, username, expiresAt)
	return err
}
func SessionStoreAvailable() bool { return currentStore() != nil }
func LoadSession(tokenHash string) (string, bool) {
	current := currentStore()
	if current == nil {
		return "", false
	}
	var username string
	err := current.QueryRow(`SELECT username FROM user_sessions WHERE token_hash = ? AND expires_at > CURRENT_TIMESTAMP`, tokenHash).Scan(&username)
	return username, err == nil
}
func DeleteSession(tokenHash string) {
	if current := currentStore(); current != nil {
		_, _ = current.Exec(`DELETE FROM user_sessions WHERE token_hash = ?`, tokenHash)
	}
}
func PruneSessions() {
	if current := currentStore(); current != nil {
		_, _ = current.Exec(`DELETE FROM user_sessions WHERE expires_at <= CURRENT_TIMESTAMP`)
	}
}
func RecordSecurityEvent(eventType, username, remoteAddr, details string) {
	action := map[string]string{"login_succeeded": "登录成功", "login_failed": "登录失败", "login_rate_limited": "登录受限"}[eventType]
	if action == "" {
		action = "安全事件"
	}
	detail := map[string]string{"invalid credentials": "账号或密码错误", "too many failed attempts": "登录失败次数过多"}[details]
	if detail == "" {
		detail = details
	}
	RecordOperation(username, action, detail, remoteAddr)
}

type AuditLogItem struct {
	ID         int64  `json:"id"`
	Category   string `json:"category"`
	Action     string `json:"action"`
	Username   string `json:"username"`
	RemoteAddr string `json:"remoteAddr"`
	Details    string `json:"details"`
	CreatedAt  string `json:"createdAt"`
}

type AuditLogQuery struct {
	Action   string
	Username string
	Keyword  string
	Page     int
	PageSize int
}

type AuditLogPage struct {
	Items     []AuditLogItem `json:"items"`
	Total     int64          `json:"total"`
	Page      int            `json:"page"`
	PageSize  int            `json:"pageSize"`
	Actions   []string       `json:"actions"`
	Usernames []string       `json:"usernames"`
}

func RecordOperation(username, action, resource, remoteAddr string) {
	if current := currentStore(); current != nil {
		_, _ = current.Exec(`INSERT INTO audit_logs (username, action, details, remote_addr) VALUES (?, ?, ?, ?)`, username, action, resource, remoteAddr)
	}
}
func ListAuditLogs(query AuditLogQuery) AuditLogPage {
	current := currentStore()
	if current == nil {
		return AuditLogPage{Items: []AuditLogItem{}, Actions: []string{}, Usernames: []string{}}
	}
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 || query.PageSize > 200 {
		query.PageSize = 20
	}
	where, args := " WHERE 1 = 1", make([]any, 0, 3)
	if query.Action != "" {
		where += " AND action = ?"
		args = append(args, query.Action)
	}
	if query.Username != "" {
		where += " AND username = ?"
		args = append(args, query.Username)
	}
	if query.Keyword != "" {
		where += " AND (details LIKE ? OR action LIKE ?)"
		keyword := "%" + query.Keyword + "%"
		args = append(args, keyword, keyword)
	}
	page := AuditLogPage{Items: make([]AuditLogItem, 0, query.PageSize), Page: query.Page, PageSize: query.PageSize, Actions: []string{}, Usernames: []string{}}
	if err := current.QueryRow("SELECT COUNT(*) FROM audit_logs"+where, args...).Scan(&page.Total); err != nil {
		return page
	}
	rows, err := current.Query(`SELECT id, 'audit', action, username, remote_addr, details, created_at FROM audit_logs`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, query.PageSize, (query.Page-1)*query.PageSize)...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var item AuditLogItem
			var at time.Time
			if rows.Scan(&item.ID, &item.Category, &item.Action, &item.Username, &item.RemoteAddr, &item.Details, &at) == nil {
				item.CreatedAt = at.Format("2006-01-02 15:04:05")
				page.Items = append(page.Items, item)
			}
		}
	}
	for _, target := range []struct {
		column string
		output *[]string
	}{{"action", &page.Actions}, {"username", &page.Usernames}} {
		rows, err := current.Query("SELECT DISTINCT " + target.column + " FROM audit_logs WHERE " + target.column + " <> '' ORDER BY " + target.column)
		if err != nil {
			continue
		}
		for rows.Next() {
			var value string
			if rows.Scan(&value) == nil {
				*target.output = append(*target.output, value)
			}
		}
		rows.Close()
	}
	return page
}
