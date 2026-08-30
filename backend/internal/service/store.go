package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"monitor-platform/internal/model"
)

const defaultDatabase = "opsguard"

var (
	mu    sync.RWMutex
	db    *sql.DB
	rules []model.CollectionRule

	collectionRuleEvaluatorOnce sync.Once
)

func init() {
	rules = GetCollectionRules()
}

func currentStore() *sql.DB {
	mu.RLock()
	defer mu.RUnlock()
	return db
}

func normalizeLimit(limit int, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func InitDataSourceStore() error {
	host := getEnv("MYSQL_HOST", "rm-bp16f9ux6a109l00p1o.mysql.rds.aliyuncs.com")
	port := getEnv("MYSQL_PORT", "3306")
	user := getEnv("MYSQL_USER", "opsguard_app")
	password := getEnv("MYSQL_PASSWORD", "")
	database := getEnv("MYSQL_DATABASE", defaultDatabase)
	if password == "" {
		return errors.New("MYSQL_PASSWORD is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := initMySQLMetricStore(host, port, user, password, database); err != nil {
		return err
	}

	appDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=Local&timeout=5s&readTimeout=8s&writeTimeout=8s", user, password, host, port, database)
	appDB, err := sql.Open("mysql", appDSN)
	if err != nil {
		return err
	}
	appDB.SetMaxOpenConns(10)
	appDB.SetMaxIdleConns(5)
	appDB.SetConnMaxLifetime(5 * time.Minute)
	if err := appDB.PingContext(ctx); err != nil {
		return err
	}

	schema := `CREATE TABLE IF NOT EXISTS data_sources (
		id varchar(64) PRIMARY KEY,
		name varchar(120) NOT NULL,
		type varchar(40) NOT NULL,
		host varchar(255) NOT NULL,
		port varchar(16) NOT NULL,
		username varchar(120) NULL,
		password varchar(255) NULL,
		database_name varchar(120) NULL,
		remark text NULL,
			options_json json NULL,
			enabled tinyint(1) NOT NULL DEFAULT 1,
			status varchar(32) NOT NULL DEFAULT '待测试',
		last_test varchar(64) NOT NULL DEFAULT '未测试',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`
	if _, err := appDB.ExecContext(ctx, schema); err != nil {
		return err
	}
	_, _ = appDB.ExecContext(ctx, `ALTER TABLE data_sources MODIFY database_name text NULL`)
	_, _ = appDB.ExecContext(ctx, `ALTER TABLE data_sources ADD COLUMN enabled tinyint(1) NOT NULL DEFAULT 1 AFTER options_json`)
	accountSchema := `CREATE TABLE IF NOT EXISTS users (
		username varchar(64) PRIMARY KEY,
		password varchar(255) NOT NULL,
		display_name varchar(120) NOT NULL,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`
	if _, err := appDB.ExecContext(ctx, accountSchema); err != nil {
		return err
	}
	if _, err := appDB.ExecContext(ctx, `INSERT IGNORE INTO users (username, password, display_name) VALUES ('admin', 'admin@123', '平台管理员')`); err != nil {
		return err
	}
	if err := initCollectionRuleStore(appDB); err != nil {
		return err
	}
	ensureDefaultPrometheusDataSource(appDB)

	mu.Lock()
	db = appDB
	mu.Unlock()
	go startDataSourceHealthChecker()
	startMySQLMetricCollector()
	startCollectionRuleEvaluator()
	return nil
}

func AuthenticateUser(username string, password string) bool {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return username == "admin" && password == "admin@123"
	}

	var stored string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := current.QueryRowContext(ctx, `SELECT password FROM users WHERE username = ?`, username).Scan(&stored); err != nil {
		return false
	}
	return password == stored
}

func ChangeUserPassword(username string, oldPassword string, newPassword string) error {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return errors.New("account store is not initialized")
	}
	if !AuthenticateUser(username, oldPassword) {
		return errors.New("原密码错误")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := current.ExecContext(ctx, `UPDATE users SET password = ? WHERE username = ?`, newPassword, username)
	return err
}

func ListDataSources() []model.DataSource {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return []model.DataSource{}
	}

	rows, err := current.Query(`SELECT id, name, type, host, port, COALESCE(username, ''), COALESCE(database_name, ''), COALESCE(remark, ''), COALESCE(options_json, '{}'), enabled, status, last_test FROM data_sources ORDER BY created_at DESC`)
	if err != nil {
		return []model.DataSource{}
	}
	defer rows.Close()

	sources := make([]model.DataSource, 0)
	for rows.Next() {
		var ds model.DataSource
		var optionsRaw string
		if err := rows.Scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Database, &ds.Remark, &optionsRaw, &ds.Enabled, &ds.Status, &ds.LastTest); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(optionsRaw), &ds.Options)
		ds.Password = ""
		sources = append(sources, ds)
	}
	return sources
}

func AddDataSource(ds model.DataSource) (model.DataSource, error) {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return model.DataSource{}, errors.New("data source store is not initialized")
	}

	ds.Name = strings.TrimSpace(ds.Name)
	ds.Type = strings.TrimSpace(ds.Type)
	ds.Host = strings.TrimSpace(ds.Host)
	ds.Port = strings.TrimSpace(ds.Port)
	if ds.Name == "" || ds.Type == "" || ds.Host == "" || ds.Port == "" {
		return model.DataSource{}, errors.New("name, type, host and port are required")
	}
	if !isSupportedDataSourceType(ds.Type) {
		return model.DataSource{}, errors.New("目前仅支持 Prometheus 和 MySQL 数据源")
	}
	if ok, msg := TestDataSourceConnection(ds); !ok {
		return model.DataSource{}, errors.New(msg)
	}
	if ds.ID == "" {
		ds.ID = fmt.Sprintf("ds-%d", time.Now().UnixNano())
	}
	ds.Status = "健康"
	ds.Enabled = true
	ds.LastTest = time.Now().Format("2006-01-02 15:04")
	optionsJSON, err := json.Marshal(ds.Options)
	if err != nil {
		return model.DataSource{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = current.ExecContext(ctx, `INSERT INTO data_sources
		(id, name, type, host, port, username, password, database_name, remark, options_json, enabled, status, last_test)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ds.ID, ds.Name, ds.Type, ds.Host, ds.Port, ds.Username, ds.Password, ds.Database, ds.Remark, string(optionsJSON), ds.Enabled, ds.Status, ds.LastTest)
	if err != nil {
		return model.DataSource{}, err
	}
	ds.Password = ""
	return ds, nil
}

func UpdateDataSource(id string, ds model.DataSource) (model.DataSource, error) {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return model.DataSource{}, errors.New("data source store is not initialized")
	}

	ds.Name = strings.TrimSpace(ds.Name)
	ds.Type = strings.TrimSpace(ds.Type)
	ds.Host = strings.TrimSpace(ds.Host)
	ds.Port = strings.TrimSpace(ds.Port)
	if id == "" || ds.Name == "" || ds.Type == "" || ds.Host == "" || ds.Port == "" {
		return model.DataSource{}, errors.New("id, name, type, host and port are required")
	}
	optionsJSON, err := json.Marshal(ds.Options)
	if err != nil {
		return model.DataSource{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	existing, err := GetDataSourceByID(id)
	if err != nil {
		return model.DataSource{}, errors.New("data source not found")
	}
	ds.Enabled = existing.Enabled
	if !isSupportedDataSourceType(ds.Type) {
		return model.DataSource{}, errors.New("目前仅支持 Prometheus 和 MySQL 数据源")
	}
	if ok, msg := TestDataSourceConnection(ds); !ok {
		return model.DataSource{}, errors.New(msg)
	}
	ds.Status = "健康"
	ds.LastTest = time.Now().Format("2006-01-02 15:04")
	if ds.Password == "" {
		_, err = current.ExecContext(ctx, `UPDATE data_sources
			SET name = ?, type = ?, host = ?, port = ?, username = ?, database_name = ?, remark = ?, options_json = ?, status = ?, last_test = ?
			WHERE id = ?`,
			ds.Name, ds.Type, ds.Host, ds.Port, ds.Username, ds.Database, ds.Remark, string(optionsJSON), ds.Status, ds.LastTest, id)
	} else {
		_, err = current.ExecContext(ctx, `UPDATE data_sources
			SET name = ?, type = ?, host = ?, port = ?, username = ?, password = ?, database_name = ?, remark = ?, options_json = ?, status = ?, last_test = ?
			WHERE id = ?`,
			ds.Name, ds.Type, ds.Host, ds.Port, ds.Username, ds.Password, ds.Database, ds.Remark, string(optionsJSON), ds.Status, ds.LastTest, id)
	}
	if err != nil {
		return model.DataSource{}, err
	}
	updated, err := GetDataSourceByID(id)
	if err != nil {
		return model.DataSource{}, err
	}
	return updated, nil
}

func DeleteDataSource(id string) error {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return errors.New("data source store is not initialized")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("id is required")
	}

	existing, err := GetDataSourceByID(id)
	if err != nil {
		return errors.New("data source not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !strings.EqualFold(existing.Type, "prometheus") {
		_, _ = current.ExecContext(ctx, `DELETE FROM collection_rules WHERE source = ? OR source = ?`, existing.ID, existing.Name)
		_, _ = current.ExecContext(ctx, `DELETE FROM alert_notifications WHERE source = ? OR source = ?`, existing.ID, existing.Name)
	}
	result, err := current.ExecContext(ctx, `DELETE FROM data_sources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return errors.New("data source not found")
	}
	return nil
}

func getDataSourcePassword(id string) (string, error) {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return "", errors.New("data source store is not initialized")
	}
	var password string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := current.QueryRowContext(ctx, `SELECT COALESCE(password, '') FROM data_sources WHERE id = ?`, id).Scan(&password)
	return password, err
}

func GetDataSourceByID(id string) (model.DataSource, error) {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return model.DataSource{}, errors.New("data source store is not initialized")
	}

	var ds model.DataSource
	var optionsRaw string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := current.QueryRowContext(ctx, `SELECT id, name, type, host, port, COALESCE(username, ''), COALESCE(database_name, ''), COALESCE(remark, ''), COALESCE(options_json, '{}'), enabled, status, last_test FROM data_sources WHERE id = ?`, id).
		Scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Database, &ds.Remark, &optionsRaw, &ds.Enabled, &ds.Status, &ds.LastTest)
	if err != nil {
		return model.DataSource{}, err
	}
	_ = json.Unmarshal([]byte(optionsRaw), &ds.Options)
	return ds, nil
}

func SetDataSourceEnabled(id string, enabled bool) (model.DataSource, error) {
	id = strings.Trim(strings.TrimSuffix(strings.TrimSpace(id), "/enabled"), "/")
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return model.DataSource{}, errors.New("data source store is not initialized")
	}
	existing, err := GetDataSourceByID(id)
	if err != nil {
		return model.DataSource{}, errors.New("data source not found")
	}
	status := existing.Status
	if !enabled {
		status = "停用"
	} else if status == "停用" {
		status = "待采集"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = current.ExecContext(ctx, `UPDATE data_sources SET enabled = ?, status = ? WHERE id = ?`, enabled, status, id)
	if err != nil {
		return model.DataSource{}, err
	}
	return GetDataSourceByID(id)
}

func TestDataSourceConnection(ds model.DataSource) (bool, string) {
	ds.Type = strings.TrimSpace(ds.Type)
	ds.Host = strings.TrimSpace(ds.Host)
	ds.Port = strings.TrimSpace(ds.Port)
	if strings.TrimSpace(ds.Password) == "" && strings.TrimSpace(ds.ID) != "" {
		_ = FillDataSourcePassword(&ds)
	}
	if ds.Host == "" || ds.Port == "" {
		return false, "主机地址和端口不能为空"
	}
	switch {
	case strings.EqualFold(ds.Type, "prometheus"):
		if err := TestPrometheusDataSource(ds); err != nil {
			return false, err.Error()
		}
		return true, "Prometheus 连接测试成功"
	case strings.EqualFold(ds.Type, "mysql"):
		if strings.TrimSpace(ds.Username) == "" {
			return false, "MySQL 用户名不能为空"
		}
		if strings.TrimSpace(ds.Password) == "" {
			return false, "MySQL 密码不能为空"
		}
		if err := TestMySQLDataSource(ds); err != nil {
			return false, err.Error()
		}
		return true, "MySQL 连接测试成功"
	default:
		return false, "目前仅支持 Prometheus 和 MySQL 数据源"
	}
}

func isSupportedDataSourceType(sourceType string) bool {
	return strings.EqualFold(sourceType, "prometheus") || strings.EqualFold(sourceType, "mysql")
}

func FillDataSourcePassword(ds *model.DataSource) error {
	if ds == nil || strings.TrimSpace(ds.ID) == "" {
		return errors.New("data source id is required")
	}
	password, err := getDataSourcePassword(ds.ID)
	if err != nil {
		return errors.New("data source password not found")
	}
	ds.Password = password
	return nil
}

func primaryDatabase(value string) string {
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ListDataSourceDatabases(ds model.DataSource) ([]string, error) {
	return []string{}, nil
}

func startDataSourceHealthChecker() {
	checkAllDataSourceConnections()
	ticker := time.NewTicker(60 * time.Second)
	for range ticker.C {
		checkAllDataSourceConnections()
	}
}

func RefreshDataSourceHealth() []model.DataSource {
	checkAllDataSourceConnections()
	return ListDataSources()
}

func checkAllDataSourceConnections() {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return
	}

	rows, err := current.Query(`SELECT id, type, host, port, COALESCE(username, ''), COALESCE(password, ''), COALESCE(database_name, '') FROM data_sources WHERE enabled = 1`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var ds model.DataSource
		if err := rows.Scan(&ds.ID, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Password, &ds.Database); err != nil {
			continue
		}
		ok, _ := TestDataSourceConnection(ds)
		status := "异常"
		if ok {
			status = "健康"
		}
		_, _ = current.Exec(`UPDATE data_sources SET status = ?, last_test = ? WHERE id = ?`, status, time.Now().Format("2006-01-02 15:04"), ds.ID)
	}
}

func ListCollectionRules() []model.CollectionRule {
	current := currentStore()
	if current == nil {
		mu.RLock()
		defer mu.RUnlock()
		res := make([]model.CollectionRule, len(rules))
		copy(res, rules)
		return res
	}
	rows, err := current.Query(`SELECT id, name, source, database_name, table_name, field_name, condition_text,
		COALESCE(threshold, ''), time_window, last_run, status FROM collection_rules ORDER BY created_at DESC`)
	if err != nil {
		return []model.CollectionRule{}
	}
	defer rows.Close()
	items := []model.CollectionRule{}
	for rows.Next() {
		var rule model.CollectionRule
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Source, &rule.Database, &rule.Table, &rule.Field, &rule.Condition, &rule.Threshold, &rule.TimeWindow, &rule.LastRun, &rule.Status); err != nil {
			continue
		}
		items = append(items, rule)
	}
	return items
}

func AddCollectionRule(rule model.CollectionRule) (model.CollectionRule, error) {
	current := currentStore()
	rule = normalizeCollectionRule(rule)
	if err := validateCollectionRuleSourceEnabled(rule); err != nil {
		return model.CollectionRule{}, err
	}
	if current == nil {
		mu.Lock()
		defer mu.Unlock()
		rules = append([]model.CollectionRule{rule}, rules...)
		return rule, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := current.ExecContext(ctx, `INSERT INTO collection_rules
		(id, name, source, database_name, table_name, field_name, condition_text, threshold, time_window, last_run, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.Name, rule.Source, rule.Database, rule.Table, rule.Field, rule.Condition, rule.Threshold, rule.TimeWindow, rule.LastRun, rule.Status)
	return rule, err
}

func UpdateCollectionRule(id string, rule model.CollectionRule) (model.CollectionRule, error) {
	current := currentStore()
	if current == nil {
		return model.CollectionRule{}, errors.New("collection rule store is not initialized")
	}
	rule.ID = id
	rule = normalizeCollectionRule(rule)
	if err := validateCollectionRuleSourceEnabled(rule); err != nil {
		return model.CollectionRule{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := current.ExecContext(ctx, `UPDATE collection_rules
		SET name = ?, source = ?, database_name = ?, table_name = ?, field_name = ?, condition_text = ?,
			threshold = ?, time_window = ?, status = ?
		WHERE id = ?`,
		rule.Name, rule.Source, rule.Database, rule.Table, rule.Field, rule.Condition, rule.Threshold, rule.TimeWindow, rule.Status, id)
	if err != nil {
		return model.CollectionRule{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return model.CollectionRule{}, errors.New("collection rule not found")
	}
	return rule, nil
}

func DeleteCollectionRule(id string) error {
	current := currentStore()
	if current == nil {
		return errors.New("collection rule store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := current.ExecContext(ctx, `DELETE FROM collection_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return errors.New("collection rule not found")
	}
	return nil
}

func ListAlertNotifications(status string, unread string, limit int) []model.AlertNotification {
	current := currentStore()
	if current == nil {
		return []model.AlertNotification{}
	}
	limit = normalizeLimit(limit, 100)
	conditions := []string{"1 = 1"}
	args := []any{}
	status = strings.TrimSpace(status)
	if status != "" && status != "all" {
		conditions = append(conditions, "status = ?")
		args = append(args, status)
	}
	if unread == "1" || strings.EqualFold(unread, "true") {
		conditions = append(conditions, "unread = 1")
	}
	args = append(args, limit)
	rows, err := current.Query(`SELECT id, rule_id, rule_name, source, database_name, table_name, field_name,
		severity, status, message, unread, first_seen_at, last_seen_at, resolved_at
		FROM alert_notifications WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY last_seen_at DESC LIMIT ?`, args...)
	if err != nil {
		return []model.AlertNotification{}
	}
	defer rows.Close()
	items := []model.AlertNotification{}
	for rows.Next() {
		var item model.AlertNotification
		var resolvedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.RuleID, &item.RuleName, &item.Source, &item.Database, &item.Table, &item.Field,
			&item.Severity, &item.Status, &item.Message, &item.Unread, &item.FirstSeenAt, &item.LastSeenAt, &resolvedAt); err != nil {
			continue
		}
		if resolvedAt.Valid {
			item.ResolvedAt = &resolvedAt.Time
		}
		items = append(items, item)
	}
	return items
}

func AlertNotificationUnreadCount() int64 {
	current := currentStore()
	if current == nil {
		return 0
	}
	var count int64
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = current.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notifications WHERE unread = 1`).Scan(&count)
	return count
}

func MarkAlertNotificationsRead(id string) error {
	current := currentStore()
	if current == nil {
		return errors.New("notification store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if strings.TrimSpace(id) == "" || id == "all" {
		_, err := current.ExecContext(ctx, `UPDATE alert_notifications SET unread = 0 WHERE unread = 1`)
		return err
	}
	result, err := current.ExecContext(ctx, `UPDATE alert_notifications SET unread = 0 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return errors.New("notification not found")
	}
	return nil
}

func initCollectionRuleStore(appDB *sql.DB) error {
	if _, err := appDB.Exec(`CREATE TABLE IF NOT EXISTS collection_rules (
		id varchar(64) PRIMARY KEY,
		name varchar(120) NOT NULL,
		source varchar(80) NOT NULL,
		database_name varchar(120) NOT NULL DEFAULT '',
		table_name varchar(120) NOT NULL DEFAULT '',
		field_name varchar(120) NOT NULL DEFAULT '',
		condition_text varchar(120) NOT NULL,
		threshold varchar(80) NULL,
		time_window varchar(80) NOT NULL DEFAULT '5分钟',
		last_run varchar(64) NOT NULL DEFAULT '待执行',
		status varchar(32) NOT NULL DEFAULT '启用',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}
	_, err := appDB.Exec(`CREATE TABLE IF NOT EXISTS alert_notifications (
		id varchar(96) PRIMARY KEY,
		rule_id varchar(64) NOT NULL,
		rule_name varchar(120) NOT NULL,
		source varchar(120) NOT NULL,
		database_name varchar(120) NOT NULL DEFAULT '',
		table_name varchar(120) NOT NULL DEFAULT '',
		field_name varchar(120) NOT NULL DEFAULT '',
		severity varchar(32) NOT NULL,
		status varchar(32) NOT NULL,
		message text NOT NULL,
		unread tinyint(1) NOT NULL DEFAULT 1,
		first_seen_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_seen_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		resolved_at timestamp NULL,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		INDEX idx_alert_notifications_status_time (status, last_seen_at),
		INDEX idx_alert_notifications_unread (unread)
	)`)
	return err
}

func clearCollectionRules(appDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = appDB.ExecContext(ctx, `DELETE FROM collection_rules`)
	_, _ = appDB.ExecContext(ctx, `DELETE FROM alert_notifications`)
}

func ensureDefaultPrometheusDataSource(appDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = appDB.ExecContext(ctx, `INSERT INTO data_sources
		(id, name, type, host, port, username, password, database_name, remark, options_json, enabled, status, last_test)
		VALUES ('prometheus-local', '本机 Prometheus', 'Prometheus', '127.0.0.1', '9090', '', '', '', 'docker-compose 部署的 Prometheus', '{}', 1, '健康', ?)
		ON DUPLICATE KEY UPDATE type = VALUES(type), host = VALUES(host), port = VALUES(port), remark = VALUES(remark), enabled = 1`,
		time.Now().Format("2006-01-02 15:04"))
}

func startCollectionRuleEvaluator() {
	collectionRuleEvaluatorOnce.Do(func() {
		go func() {
			evaluateCollectionRules()
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				evaluateCollectionRules()
			}
		}()
	})
}

func evaluateCollectionRules() {
	current := currentStore()
	if current == nil {
		return
	}
	rows, err := current.Query(`SELECT id, name, source, database_name, table_name, field_name, condition_text, COALESCE(threshold, ''), time_window
		FROM collection_rules WHERE status = '启用'`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var rule model.CollectionRule
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Source, &rule.Database, &rule.Table, &rule.Field, &rule.Condition, &rule.Threshold, &rule.TimeWindow); err != nil {
			continue
		}
		lastRun := evaluateCollectionRule(rule)
		_, _ = current.Exec(`UPDATE collection_rules SET last_run = ? WHERE id = ?`, lastRun, rule.ID)
		syncAlertNotification(current, rule, lastRun)
	}
}

func evaluateCollectionRule(rule model.CollectionRule) string {
	if isCustomProbeRule(rule) {
		return evaluateCustomProbeRule(rule)
	}
	if rule.Condition == "当天有数据" {
		return evaluateTodayHasDataRule(rule)
	}
	return evaluateMetricThresholdRule(rule)
}

func isCustomProbeRule(rule model.CollectionRule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.Source), "custom-probe")
}

func evaluateMetricThresholdRule(rule model.CollectionRule) string {
	now := time.Now()
	checkedAt := now.Format("15:04")
	ds, err := getRuleDataSourceWithSecret(rule.Source)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：%s", checkedAt, err.Error())
	}
	if !strings.EqualFold(ds.Type, "prometheus") {
		return fmt.Sprintf("等待 %s：普通阈值规则评估待接入", checkedAt)
	}
	data, err := QueryPrometheusDataSourceByID(ds.ID, rule.Table)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：Prometheus 查询失败：%s", checkedAt, err.Error())
	}
	value, ok := firstPrometheusVectorValue(data)
	if !ok {
		return fmt.Sprintf("执行失败 %s：Prometheus 查询无数据", checkedAt)
	}
	threshold, err := strconv.ParseFloat(strings.TrimSpace(rule.Threshold), 64)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：阈值必须是数字", checkedAt)
	}
	matched := false
	switch rule.Condition {
	case "小于":
		matched = value < threshold
	case "等于":
		matched = value == threshold
	default:
		matched = value > threshold
	}
	if matched {
		return fmt.Sprintf("告警 %s：Prometheus 指标 %.4f %s %.4f", checkedAt, value, rule.Condition, threshold)
	}
	return fmt.Sprintf("正常 %s：Prometheus 指标 %.4f 未触发阈值", checkedAt, value)
}

func firstPrometheusVectorValue(data any) (float64, bool) {
	payload, ok := data.(map[string]any)
	if !ok {
		return 0, false
	}
	result, ok := payload["result"].([]any)
	if !ok || len(result) == 0 {
		return 0, false
	}
	row, ok := result[0].(map[string]any)
	if !ok {
		return 0, false
	}
	value, ok := row["value"].([]any)
	if !ok || len(value) < 2 {
		return 0, false
	}
	text, ok := value[1].(string)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(text, 64)
	return parsed, err == nil
}

func evaluateCustomProbeRule(rule model.CollectionRule) string {
	now := time.Now()
	checkedAt := now.Format("15:04")
	probeType := strings.ToLower(strings.TrimSpace(rule.Database))
	condition := strings.TrimSpace(rule.Condition)
	target := strings.TrimSpace(rule.Table)
	timeout := probeTimeout(rule.TimeWindow)
	if target == "" {
		return fmt.Sprintf("执行失败 %s：探测目标不能为空", checkedAt)
	}
	switch probeType {
	case "http", "https", "http页面":
		return evaluateHTTPProbe(rule, target, condition, timeout, checkedAt)
	case "tcp", "tcp端口":
		return evaluateTCPProbe(target, timeout, checkedAt)
	default:
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			return evaluateHTTPProbe(rule, target, condition, timeout, checkedAt)
		}
		return evaluateTCPProbe(target, timeout, checkedAt)
	}
}

func evaluateHTTPProbe(rule model.CollectionRule, target string, condition string, timeout time.Duration, checkedAt string) string {
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "http://" + target
	}
	parsed, err := url.ParseRequestURI(target)
	if err != nil || parsed.Host == "" {
		return fmt.Sprintf("执行失败 %s：URL 不合法", checkedAt)
	}
	client := http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Get(target)
	if err != nil {
		return fmt.Sprintf("告警 %s：HTTP 探测失败：%s", checkedAt, err.Error())
	}
	defer resp.Body.Close()
	latency := time.Since(start)
	bodyLimit := int64(256 * 1024)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if condition == "页面包含" {
		expected := strings.TrimSpace(rule.Threshold)
		if expected == "" {
			return fmt.Sprintf("执行失败 %s：页面包含规则需要填写期望内容", checkedAt)
		}
		if !strings.Contains(string(body), expected) {
			return fmt.Sprintf("告警 %s：页面未包含“%s”，状态码 %d，耗时 %dms", checkedAt, expected, resp.StatusCode, latency.Milliseconds())
		}
		return fmt.Sprintf("正常 %s：页面包含“%s”，状态码 %d，耗时 %dms", checkedAt, expected, resp.StatusCode, latency.Milliseconds())
	}
	if condition == "状态码等于" {
		expected, err := strconv.Atoi(strings.TrimSpace(rule.Threshold))
		if err != nil {
			return fmt.Sprintf("执行失败 %s：状态码阈值必须是数字", checkedAt)
		}
		if resp.StatusCode != expected {
			return fmt.Sprintf("告警 %s：HTTP 状态码 %d，不等于 %d，耗时 %dms", checkedAt, resp.StatusCode, expected, latency.Milliseconds())
		}
		return fmt.Sprintf("正常 %s：HTTP 状态码 %d，耗时 %dms", checkedAt, resp.StatusCode, latency.Milliseconds())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Sprintf("告警 %s：HTTP 状态码 %d，耗时 %dms", checkedAt, resp.StatusCode, latency.Milliseconds())
	}
	return fmt.Sprintf("正常 %s：HTTP 状态码 %d，耗时 %dms", checkedAt, resp.StatusCode, latency.Milliseconds())
}

func evaluateTCPProbe(target string, timeout time.Duration, checkedAt string) string {
	if !strings.Contains(target, ":") {
		return fmt.Sprintf("执行失败 %s：TCP 探测目标必须是 host:port", checkedAt)
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return fmt.Sprintf("告警 %s：TCP 连接失败：%s", checkedAt, err.Error())
	}
	_ = conn.Close()
	return fmt.Sprintf("正常 %s：TCP 端口可连接，耗时 %dms", checkedAt, time.Since(start).Milliseconds())
}

func probeTimeout(value string) time.Duration {
	text := strings.TrimSpace(strings.ReplaceAll(value, "秒", "s"))
	if text == "" {
		return 5 * time.Second
	}
	if duration, err := time.ParseDuration(text); err == nil {
		if duration < time.Second {
			return time.Second
		}
		if duration > 30*time.Second {
			return 30 * time.Second
		}
		return duration
	}
	return 5 * time.Second
}

func syncAlertNotification(appDB *sql.DB, rule model.CollectionRule, lastRun string) {
	if strings.HasPrefix(lastRun, "告警") || strings.HasPrefix(lastRun, "执行失败") {
		severity := "warning"
		if strings.HasPrefix(lastRun, "执行失败") {
			severity = "critical"
		}
		upsertAlertNotification(appDB, rule, severity, "active", lastRun)
		return
	}
	if strings.HasPrefix(lastRun, "正常") {
		resolveAlertNotification(appDB, rule.ID)
	}
}

func upsertAlertNotification(appDB *sql.DB, rule model.CollectionRule, severity string, status string, message string) {
	day := time.Now().Format("20060102")
	id := fmt.Sprintf("%s-%s", rule.ID, day)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = appDB.ExecContext(ctx, `INSERT INTO alert_notifications
		(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, NOW(), NOW())
		ON DUPLICATE KEY UPDATE rule_name = VALUES(rule_name), source = VALUES(source), database_name = VALUES(database_name),
			table_name = VALUES(table_name), field_name = VALUES(field_name), severity = VALUES(severity),
			status = VALUES(status), message = VALUES(message), unread = 1, last_seen_at = NOW(), resolved_at = NULL`,
		id, rule.ID, rule.Name, rule.Source, rule.Database, rule.Table, rule.Field, severity, status, message)
}

func resolveAlertNotification(appDB *sql.DB, ruleID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = appDB.ExecContext(ctx, `UPDATE alert_notifications
		SET status = 'resolved', message = CONCAT('已恢复：', message), resolved_at = IFNULL(resolved_at, NOW()), last_seen_at = NOW()
		WHERE rule_id = ? AND status = 'active'`, ruleID)
}

func evaluateTodayHasDataRule(rule model.CollectionRule) string {
	now := time.Now()
	checkedAt := now.Format("15:04")
	deadline := todayDataRuleDeadline(rule.TimeWindow, now)
	ds, err := getRuleDataSourceWithSecret(rule.Source)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：%s", checkedAt, err.Error())
	}
	if !strings.EqualFold(ds.Type, "mysql") {
		return fmt.Sprintf("执行失败 %s：当天有数据规则仅支持 MySQL 数据源", checkedAt)
	}
	if !validSQLIdentifier(rule.Database) || !validSQLIdentifier(rule.Table) {
		return fmt.Sprintf("执行失败 %s：库名或表名不合法", checkedAt)
	}
	if rule.Field != "" && !validSQLIdentifier(rule.Field) {
		return fmt.Sprintf("执行失败 %s：字段名不合法", checkedAt)
	}
	targetDB, err := openMySQLDataSource(ds)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：%s", checkedAt, err.Error())
	}
	defer targetDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	query := fmt.Sprintf("SELECT COUNT(*) FROM `%s`.`%s`", rule.Database, rule.Table)
	args := []any{}
	if rule.Field != "" {
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		end := start.AddDate(0, 0, 1)
		query += fmt.Sprintf(" WHERE `%s` >= ? AND `%s` < ?", rule.Field, rule.Field)
		args = append(args, start, end)
	}
	var count int64
	if err := targetDB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return fmt.Sprintf("执行失败 %s：查询失败：%s", checkedAt, err.Error())
	}
	if count > 0 {
		return fmt.Sprintf("正常 %s：%s.%s 今日数据 %d 条", checkedAt, rule.Database, rule.Table, count)
	}
	if now.Before(deadline) {
		return fmt.Sprintf("等待 %s：%s.%s 今日暂无数据，%s 后仍无数据再告警", checkedAt, rule.Database, rule.Table, deadline.Format("15:04"))
	}
	return fmt.Sprintf("告警 %s：%s.%s 今日暂无数据", checkedAt, rule.Database, rule.Table)
}

func todayDataRuleDeadline(value string, now time.Time) time.Time {
	hour, minute := 3, 0
	text := strings.TrimSpace(value)
	for _, token := range strings.Fields(strings.ReplaceAll(text, "：", ":")) {
		if parsed, err := time.ParseInLocation("15:04", strings.Trim(token, "，,;；"), time.Local); err == nil {
			hour = parsed.Hour()
			minute = parsed.Minute()
			break
		}
	}
	return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
}

func getRuleDataSourceWithSecret(source string) (model.DataSource, error) {
	current := currentStore()
	if current == nil {
		return model.DataSource{}, errors.New("data source store is not initialized")
	}
	var ds model.DataSource
	var optionsRaw string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := current.QueryRowContext(ctx, `SELECT id, name, type, host, port, COALESCE(username, ''), COALESCE(password, ''),
		COALESCE(database_name, ''), COALESCE(remark, ''), COALESCE(options_json, '{}'), enabled, status, last_test
		FROM data_sources WHERE id = ? OR name = ? LIMIT 1`, source, source).
		Scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Password, &ds.Database, &ds.Remark, &optionsRaw, &ds.Enabled, &ds.Status, &ds.LastTest)
	if err != nil {
		return model.DataSource{}, errors.New("数据源不存在")
	}
	_ = json.Unmarshal([]byte(optionsRaw), &ds.Options)
	return ds, nil
}

func validSQLIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' {
			continue
		}
		if char >= 'A' && char <= 'Z' {
			continue
		}
		if char >= '0' && char <= '9' {
			continue
		}
		if char == '_' || char == '$' {
			continue
		}
		return false
	}
	return true
}

func normalizeCollectionRule(rule model.CollectionRule) model.CollectionRule {
	rule.ID = strings.TrimSpace(rule.ID)
	if rule.ID == "" {
		rule.ID = fmt.Sprintf("rule-%d", time.Now().UnixNano())
	}
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Source = strings.TrimSpace(rule.Source)
	rule.Database = strings.TrimSpace(rule.Database)
	rule.Table = strings.TrimSpace(rule.Table)
	rule.Field = strings.TrimSpace(rule.Field)
	rule.Condition = strings.TrimSpace(rule.Condition)
	rule.Threshold = strings.TrimSpace(rule.Threshold)
	rule.TimeWindow = strings.TrimSpace(rule.TimeWindow)
	rule.Status = strings.TrimSpace(rule.Status)
	if rule.Name == "" {
		rule.Name = "未命名告警规则"
	}
	if rule.Source == "" {
		rule.Source = "MySQL"
	}
	if rule.Condition == "" {
		rule.Condition = "大于"
	}
	if rule.TimeWindow == "" {
		rule.TimeWindow = "5分钟"
	}
	if rule.LastRun == "" {
		rule.LastRun = "待执行"
	}
	if rule.Status == "" {
		rule.Status = "启用"
	}
	return rule
}

func validateCollectionRuleSourceEnabled(rule model.CollectionRule) error {
	if rule.Status != "启用" {
		return nil
	}
	if isCustomProbeRule(rule) {
		return nil
	}
	if currentStore() == nil {
		return nil
	}
	ds, err := GetDataSourceByID(rule.Source)
	if err != nil {
		sources := ListDataSources()
		for _, source := range sources {
			if source.Name == rule.Source {
				ds = source
				err = nil
				break
			}
		}
	}
	if err != nil {
		return errors.New("启用告警规则前，请先选择有效的数据源")
	}
	if !ds.Enabled {
		return errors.New("该数据源已停止，请先启用数据源后再启用告警规则")
	}
	return nil
}

func GetSchemaForDataSource(id string, database string, table string) map[string]map[string][]string {
	ds, err := getRuleDataSourceWithSecret(id)
	if err != nil || !strings.EqualFold(ds.Type, "mysql") || !ds.Enabled {
		return map[string]map[string][]string{}
	}
	targetDB, err := openMySQLDataSource(ds)
	if err != nil {
		return map[string]map[string][]string{}
	}
	defer targetDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	database = strings.TrimSpace(database)
	table = strings.TrimSpace(table)
	if database != "" && !validSQLIdentifier(database) {
		return map[string]map[string][]string{}
	}
	if table != "" && !validSQLIdentifier(table) {
		return map[string]map[string][]string{}
	}
	if database != "" && table != "" {
		return listMySQLSchemaColumns(ctx, targetDB, database, table)
	}
	if database != "" {
		return listMySQLSchemaTables(ctx, targetDB, database)
	}
	return listMySQLSchemaDatabases(ctx, targetDB)
}

func listMySQLSchemaDatabases(ctx context.Context, targetDB *sql.DB) map[string]map[string][]string {
	rows, err := targetDB.QueryContext(ctx, `SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')
		ORDER BY schema_name`)
	if err != nil {
		return map[string]map[string][]string{}
	}
	defer rows.Close()
	result := map[string]map[string][]string{}
	for rows.Next() {
		var databaseName string
		if err := rows.Scan(&databaseName); err != nil {
			continue
		}
		result[databaseName] = map[string][]string{}
	}
	return result
}

func listMySQLSchemaTables(ctx context.Context, targetDB *sql.DB, database string) map[string]map[string][]string {
	rows, err := targetDB.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = ? AND table_type = 'BASE TABLE'
		ORDER BY table_name`, database)
	if err != nil {
		return map[string]map[string][]string{}
	}
	defer rows.Close()
	tables := map[string][]string{}
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			continue
		}
		tables[tableName] = []string{}
	}
	return map[string]map[string][]string{database: tables}
}

func listMySQLSchemaColumns(ctx context.Context, targetDB *sql.DB, database string, table string) map[string]map[string][]string {
	rows, err := targetDB.QueryContext(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, database, table)
	if err != nil {
		return map[string]map[string][]string{}
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var columnName string
		if err := rows.Scan(&columnName); err != nil {
			continue
		}
		columns = append(columns, columnName)
	}
	return map[string]map[string][]string{database: map[string][]string{table: columns}}
}

func databaseAllowed(database string, monitoredDatabases []string) bool {
	if len(monitoredDatabases) == 0 {
		return true
	}
	for _, item := range monitoredDatabases {
		if item == database {
			return true
		}
	}
	return false
}

func redisMetricSchema(database string) map[string]map[string][]string {
	databases := splitDatabaseList(database)
	if len(databases) == 0 {
		databases = []string{"db0"}
	}
	fields := []string{
		"connected_clients",
		"blocked_clients",
		"used_memory",
		"mem_fragmentation_ratio",
		"instantaneous_ops_per_sec",
		"hit_rate",
		"evicted_keys",
		"expired_keys",
		"rejected_connections",
		"slowlog_len",
		"key_count",
	}
	result := make(map[string]map[string][]string, len(databases))
	for _, databaseName := range databases {
		result[databaseName] = map[string][]string{"Redis 性能指标": fields}
	}
	return result
}

func splitDatabaseList(value string) []string {
	items := []string{}
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
