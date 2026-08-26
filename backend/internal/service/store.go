package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"monitor-platform/internal/model"
)

const dataSourceDatabase = "opsguard_lab"

var (
	mu    sync.RWMutex
	db    *sql.DB
	rules []model.CollectionRule
)

func init() {
	rules = GetCollectionRules()
}

func InitDataSourceStore() error {
	host := getEnv("MYSQL_HOST", "127.0.0.1")
	port := getEnv("MYSQL_PORT", "3306")
	user := getEnv("MYSQL_USER", "root")
	password := getEnv("MYSQL_PASSWORD", "Hh0321")

	rootDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/?parseTime=true&multiStatements=true", user, password, host, port)
	rootDB, err := sql.Open("mysql", rootDSN)
	if err != nil {
		return err
	}
	defer rootDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := rootDB.PingContext(ctx); err != nil {
		return err
	}
	if _, err := rootDB.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+dataSourceDatabase+" DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		return err
	}

	appDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=Local", user, password, host, port, dataSourceDatabase)
	appDB, err := sql.Open("mysql", appDSN)
	if err != nil {
		return err
	}
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
		status varchar(32) NOT NULL DEFAULT '待测试',
		last_test varchar(64) NOT NULL DEFAULT '未测试',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`
	if _, err := appDB.ExecContext(ctx, schema); err != nil {
		return err
	}
	_, _ = appDB.ExecContext(ctx, `ALTER TABLE data_sources MODIFY database_name text NULL`)
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
	if err := initMySQLMonitorStore(appDB); err != nil {
		return err
	}
	if err := initCollectionRuleStore(appDB); err != nil {
		return err
	}

	mu.Lock()
	db = appDB
	mu.Unlock()
	go startDataSourceHealthChecker()
	startMySQLMonitorCollector()
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

	rows, err := current.Query(`SELECT id, name, type, host, port, COALESCE(username, ''), COALESCE(database_name, ''), COALESCE(remark, ''), COALESCE(options_json, '{}'), status, last_test FROM data_sources ORDER BY created_at DESC`)
	if err != nil {
		return []model.DataSource{}
	}
	defer rows.Close()

	sources := make([]model.DataSource, 0)
	for rows.Next() {
		var ds model.DataSource
		var optionsRaw string
		if err := rows.Scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Database, &ds.Remark, &optionsRaw, &ds.Status, &ds.LastTest); err != nil {
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
	if strings.EqualFold(ds.Type, "mysql") && (strings.TrimSpace(ds.Username) == "" || strings.TrimSpace(ds.Password) == "") {
		return model.DataSource{}, errors.New("mysql username and password are required")
	}
	if ok, msg := TestDataSourceConnection(ds); !ok {
		return model.DataSource{}, errors.New(msg)
	}
	if ds.ID == "" {
		ds.ID = fmt.Sprintf("ds-%d", time.Now().UnixNano())
	}
	ds.Status = "健康"
	ds.LastTest = time.Now().Format("2006-01-02 15:04")
	optionsJSON, err := json.Marshal(ds.Options)
	if err != nil {
		return model.DataSource{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = current.ExecContext(ctx, `INSERT INTO data_sources
		(id, name, type, host, port, username, password, database_name, remark, options_json, status, last_test)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ds.ID, ds.Name, ds.Type, ds.Host, ds.Port, ds.Username, ds.Password, ds.Database, ds.Remark, string(optionsJSON), ds.Status, ds.LastTest)
	if err != nil {
		return model.DataSource{}, err
	}
	if strings.EqualFold(ds.Type, "mysql") {
		go collectMySQLInstance(current, ds)
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
	if _, err := GetDataSourceByID(id); err != nil {
		return model.DataSource{}, errors.New("data source not found")
	}
	if strings.EqualFold(ds.Type, "mysql") && strings.TrimSpace(ds.Username) == "" {
		return model.DataSource{}, errors.New("mysql username is required")
	}
	if ds.Password == "" {
		_, err = current.ExecContext(ctx, `UPDATE data_sources
			SET name = ?, type = ?, host = ?, port = ?, username = ?, database_name = ?, remark = ?, options_json = ?
			WHERE id = ?`,
			ds.Name, ds.Type, ds.Host, ds.Port, ds.Username, ds.Database, ds.Remark, string(optionsJSON), id)
	} else {
		_, err = current.ExecContext(ctx, `UPDATE data_sources
			SET name = ?, type = ?, host = ?, port = ?, username = ?, password = ?, database_name = ?, remark = ?, options_json = ?
			WHERE id = ?`,
			ds.Name, ds.Type, ds.Host, ds.Port, ds.Username, ds.Password, ds.Database, ds.Remark, string(optionsJSON), id)
	}
	if err != nil {
		return model.DataSource{}, err
	}
	updated, err := GetDataSourceByID(id)
	if err != nil {
		return model.DataSource{}, err
	}
	if strings.EqualFold(updated.Type, "mysql") {
		updated.Password = ds.Password
		if updated.Password == "" {
			if secret, err := getDataSourcePassword(id); err == nil {
				updated.Password = secret
			}
		}
		go collectMySQLInstance(current, updated)
		updated.Password = ""
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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
	err := current.QueryRowContext(ctx, `SELECT id, name, type, host, port, COALESCE(username, ''), COALESCE(database_name, ''), COALESCE(remark, ''), COALESCE(options_json, '{}'), status, last_test FROM data_sources WHERE id = ?`, id).
		Scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Database, &ds.Remark, &optionsRaw, &ds.Status, &ds.LastTest)
	if err != nil {
		return model.DataSource{}, err
	}
	_ = json.Unmarshal([]byte(optionsRaw), &ds.Options)
	return ds, nil
}

func TestDataSourceConnection(ds model.DataSource) (bool, string) {
	if strings.TrimSpace(ds.Password) == "" && strings.TrimSpace(ds.ID) != "" {
		if err := FillDataSourcePassword(&ds); err != nil {
			return false, err.Error()
		}
	}
	if strings.EqualFold(ds.Type, "mysql") {
		if strings.TrimSpace(ds.Username) == "" || strings.TrimSpace(ds.Password) == "" {
			return false, "MySQL 用户名和密码不能为空"
		}
		targetDB := primaryDatabase(ds.Database)
		if targetDB == "" {
			targetDB = "mysql"
		}
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?timeout=5s", ds.Username, ds.Password, ds.Host, ds.Port, targetDB)
		testDB, err := sql.Open("mysql", dsn)
		if err != nil {
			return false, err.Error()
		}
		defer testDB.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := testDB.PingContext(ctx); err != nil {
			return false, err.Error()
		}
		return true, "MySQL 连接测试成功"
	}

	if ds.Host == "" || ds.Port == "" {
		return false, "主机地址和端口不能为空"
	}
	return true, "连接参数已校验，驱动测试待接入"
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
	if !strings.EqualFold(ds.Type, "mysql") {
		return []string{}, nil
	}
	if strings.TrimSpace(ds.Username) == "" || strings.TrimSpace(ds.Password) == "" {
		return nil, errors.New("MySQL 用户名和密码不能为空")
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/mysql?timeout=5s", ds.Username, ds.Password, ds.Host, ds.Port)
	testDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	defer testDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := testDB.QueryContext(ctx, `SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	databases := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		databases = append(databases, name)
	}
	return databases, rows.Err()
}

func startDataSourceHealthChecker() {
	checkAllDataSourceConnections()
	ticker := time.NewTicker(30 * time.Minute)
	for range ticker.C {
		checkAllDataSourceConnections()
	}
}

func checkAllDataSourceConnections() {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return
	}

	rows, err := current.Query(`SELECT id, type, host, port, COALESCE(username, ''), COALESCE(password, ''), COALESCE(database_name, '') FROM data_sources`)
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

func initCollectionRuleStore(appDB *sql.DB) error {
	_, err := appDB.Exec(`CREATE TABLE IF NOT EXISTS collection_rules (
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
	)`)
	return err
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

func GetSchemaForDataSource(id string) map[string]map[string][]string {
	ds, err := GetDataSourceByID(id)
	if err != nil || !strings.EqualFold(ds.Type, "mysql") {
		return map[string]map[string][]string{}
	}
	password, err := getDataSourcePassword(id)
	if err != nil {
		return map[string]map[string][]string{}
	}
	ds.Password = password
	targetDB, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/information_schema?timeout=5s&parseTime=true&loc=Local", ds.Username, ds.Password, ds.Host, ds.Port))
	if err != nil {
		return map[string]map[string][]string{}
	}
	defer targetDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rows, err := targetDB.QueryContext(ctx, `SELECT table_schema, table_name, column_name
		FROM information_schema.columns
		WHERE table_schema NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')
		ORDER BY table_schema, table_name, ordinal_position`)
	if err != nil {
		return map[string]map[string][]string{}
	}
	defer rows.Close()
	result := map[string]map[string][]string{}
	for rows.Next() {
		var databaseName, tableName, columnName string
		if err := rows.Scan(&databaseName, &tableName, &columnName); err != nil {
			continue
		}
		if result[databaseName] == nil {
			result[databaseName] = map[string][]string{}
		}
		result[databaseName][tableName] = append(result[databaseName][tableName], columnName)
	}
	return result
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
