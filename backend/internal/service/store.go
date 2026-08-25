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
	if strings.EqualFold(ds.Type, "mysql") {
		if strings.TrimSpace(ds.Username) == "" || strings.TrimSpace(ds.Password) == "" {
			return false, "MySQL 用户名和密码不能为空"
		}
		targetDB := ds.Database
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
	mu.RLock()
	defer mu.RUnlock()
	res := make([]model.CollectionRule, len(rules))
	copy(res, rules)
	return res
}

func AddCollectionRule(rule model.CollectionRule) model.CollectionRule {
	mu.Lock()
	defer mu.Unlock()
	rules = append([]model.CollectionRule{rule}, rules...)
	return rule
}

func GetSchemaForDataSource(id string) map[string][]string {
	return map[string][]string{
		"users":    {"id", "name", "email", "created_at"},
		"orders":   {"id", "user_id", "amount", "status", "created_at"},
		"products": {"id", "sku", "title", "stock", "price"},
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
