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
	_, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS security_audit_log (
		id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
		event_type varchar(64) NOT NULL,
		username varchar(64) NOT NULL DEFAULT '',
		remote_addr varchar(255) NOT NULL DEFAULT '',
		details varchar(512) NOT NULL DEFAULT '',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_security_audit_created_at (created_at)
	)`)
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
	if current := currentStore(); current != nil {
		_, _ = current.Exec(`INSERT INTO security_audit_log (event_type, username, remote_addr, details) VALUES (?, ?, ?, ?)`, eventType, username, remoteAddr, details)
	}
}
