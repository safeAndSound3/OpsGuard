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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"monitor-platform/internal/model"
)

const defaultDatabase = "opsguard"

// The platform uses a fixed China Standard Time database session for newly
// collected samples. Rule scheduling itself uses DATETIME values written by Go
// so it remains correct even when a target MySQL server reports NOW() in UTC.
const mysqlSessionTimeZone = "&time_zone=%27%2B08%3A00%27"

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
	if err := initializeCredentialEncryption(); err != nil {
		return err
	}
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
	if err := initSSHMetricStore(); err != nil {
		return err
	}
	if err := initMiddlewareMetricStore(); err != nil {
		return err
	}

	appDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=Local&timeout=5s&readTimeout=8s&writeTimeout=8s%s", user, password, host, port, database, mysqlSessionTimeZone)
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
		password text NULL,
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
	if _, err := appDB.ExecContext(ctx, `ALTER TABLE data_sources MODIFY password text NULL`); err != nil {
		return err
	}
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
	var adminCount int
	if err := appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username = 'admin'`).Scan(&adminCount); err != nil {
		return err
	}
	if adminCount == 0 {
		initialPassword := strings.TrimSpace(os.Getenv("OPSGUARD_ADMIN_PASSWORD"))
		if len(initialPassword) < 12 {
			return errors.New("first startup requires OPSGUARD_ADMIN_PASSWORD with at least 12 characters")
		}
		hash, err := hashUserPassword(initialPassword)
		if err != nil {
			return fmt.Errorf("hash initial administrator password: %w", err)
		}
		if _, err := appDB.ExecContext(ctx, `INSERT INTO users (username, password, display_name) VALUES ('admin', ?, '平台管理员')`, hash); err != nil {
			return err
		}
	}
	if err := migrateStoredCredentials(ctx, appDB); err != nil {
		return err
	}
	if err := initCollectionRuleStore(appDB); err != nil {
		return err
	}
	if err := initNavigationStore(appDB); err != nil {
		return err
	}
	mu.Lock()
	db = appDB
	mu.Unlock()
	go startDataSourceHealthChecker()
	startMySQLMetricCollector()
	startSSHMetricCollector()
	startMiddlewareMetricCollector()
	startCollectionRuleEvaluator()
	startPrometheusAlertSynchronizer()
	return nil
}

func AuthenticateUser(username string, password string) bool {
	mu.RLock()
	current := db
	mu.RUnlock()
	if current == nil {
		return false
	}

	var stored string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := current.QueryRowContext(ctx, `SELECT password FROM users WHERE username = ?`, username).Scan(&stored); err != nil {
		return false
	}
	valid := verifyUserPassword(stored, password)
	if valid && !strings.HasPrefix(stored, "$2") {
		if hash, err := hashUserPassword(password); err == nil {
			_, _ = current.ExecContext(ctx, `UPDATE users SET password = ? WHERE username = ?`, hash, username)
		}
	}
	return valid
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
	hash, err := hashUserPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = current.ExecContext(ctx, `UPDATE users SET password = ? WHERE username = ?`, hash, username)
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
	if ds.Name == "" || ds.Type == "" || ds.Host == "" || (ds.Port == "" && !strings.EqualFold(ds.Type, "hadoop") && !strings.EqualFold(ds.Type, "ambari")) {
		return model.DataSource{}, errors.New("name, type, host and port are required")
	}
	if !isSupportedDataSourceType(ds.Type) {
		return model.DataSource{}, errors.New("目前仅支持 Prometheus、MySQL、SSH 和 Hadoop 数据源")
	}
	optionsJSON, err := json.Marshal(ds.Options)
	if err != nil {
		return model.DataSource{}, err
	}
	connectionOK, connectionMessage := TestDataSourceConnection(ds)
	if ds.ID == "" {
		ds.ID = fmt.Sprintf("ds-%d", time.Now().UnixNano())
	}
	if connectionOK {
		ds.Status = "健康"
	} else {
		ds.Status = "异常"
	}
	ds.Enabled = true
	ds.LastTest = time.Now().Format("2006-01-02 15:04")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	encryptedPassword, err := encryptCredential(ds.Password)
	if err != nil {
		return model.DataSource{}, err
	}
	_, err = current.ExecContext(ctx, `INSERT INTO data_sources
		(id, name, type, host, port, username, password, database_name, remark, options_json, enabled, status, last_test)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ds.ID, ds.Name, ds.Type, ds.Host, ds.Port, ds.Username, encryptedPassword, ds.Database, ds.Remark, string(optionsJSON), ds.Enabled, ds.Status, ds.LastTest)
	if err != nil {
		return model.DataSource{}, err
	}
	if !connectionOK {
		upsertDataSourceHealthNotification(current, ds, connectionMessage)
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
	if id == "" || ds.Name == "" || ds.Type == "" || ds.Host == "" || (ds.Port == "" && !strings.EqualFold(ds.Type, "hadoop") && !strings.EqualFold(ds.Type, "ambari")) {
		return model.DataSource{}, errors.New("id, name, type, host and port are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	existing, err := GetDataSourceByID(id)
	if err != nil {
		return model.DataSource{}, errors.New("data source not found")
	}
	ds.ID = id
	ds.Enabled = existing.Enabled
	if strings.EqualFold(ds.Type, "hadoop") {
		merged := make(map[string]string, len(existing.Options)+len(ds.Options))
		for key, value := range existing.Options {
			merged[key] = value
		}
		for key, value := range ds.Options {
			if strings.TrimSpace(value) == "" {
				delete(merged, key)
			} else {
				merged[key] = strings.TrimSpace(value)
			}
		}
		ds.Options = merged
	}
	optionsJSON, err := json.Marshal(ds.Options)
	if err != nil {
		return model.DataSource{}, err
	}
	if !isSupportedDataSourceType(ds.Type) {
		return model.DataSource{}, errors.New("目前仅支持 Prometheus、MySQL、SSH 和 Hadoop 数据源")
	}
	connectionOK, connectionMessage := TestDataSourceConnection(ds)
	if connectionOK {
		ds.Status = "健康"
	} else {
		ds.Status = "异常"
	}
	ds.LastTest = time.Now().Format("2006-01-02 15:04")
	if ds.Password == "" {
		_, err = current.ExecContext(ctx, `UPDATE data_sources
			SET name = ?, type = ?, host = ?, port = ?, username = ?, database_name = ?, remark = ?, options_json = ?, status = ?, last_test = ?
			WHERE id = ?`,
			ds.Name, ds.Type, ds.Host, ds.Port, ds.Username, ds.Database, ds.Remark, string(optionsJSON), ds.Status, ds.LastTest, id)
	} else {
		encryptedPassword, encryptErr := encryptCredential(ds.Password)
		if encryptErr != nil {
			return model.DataSource{}, encryptErr
		}
		_, err = current.ExecContext(ctx, `UPDATE data_sources
			SET name = ?, type = ?, host = ?, port = ?, username = ?, password = ?, database_name = ?, remark = ?, options_json = ?, status = ?, last_test = ?
			WHERE id = ?`,
			ds.Name, ds.Type, ds.Host, ds.Port, ds.Username, encryptedPassword, ds.Database, ds.Remark, string(optionsJSON), ds.Status, ds.LastTest, id)
	}
	if err != nil {
		return model.DataSource{}, err
	}
	if !connectionOK {
		upsertDataSourceHealthNotification(current, ds, connectionMessage)
	} else {
		resolveDataSourceHealthNotification(current, ds)
	}
	if _, err := current.ExecContext(ctx, `UPDATE dashboard_items SET name = ? WHERE source_id = ? AND name = ?`, ds.Name+" 大屏", id, existing.Name+" 大屏"); err != nil {
		return model.DataSource{}, err
	}
	if _, err := current.ExecContext(ctx, `UPDATE hadoop_menu_items SET name = ? WHERE source_id = ? AND (name = '' OR name = ?)`, ds.Name, id, existing.Name); err != nil {
		return model.DataSource{}, err
	}
	if _, err := current.ExecContext(ctx, `UPDATE ambari_menu_items SET name = ? WHERE source_id = ? AND (name = '' OR name = ?)`, ds.Name, id, existing.Name); err != nil {
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
	resolveDeletedDataSourceNotifications(current, existing)
	if !strings.EqualFold(existing.Type, "prometheus") {
		_, _ = current.ExecContext(ctx, `DELETE FROM collection_rules WHERE source = ?`, existing.ID)
	}
	_, _ = current.ExecContext(ctx, `DELETE FROM hadoop_menu_items WHERE source_id = ?`, existing.ID)
	_, _ = current.ExecContext(ctx, `DELETE FROM ambari_menu_items WHERE source_id = ?`, existing.ID)
	_, _ = current.ExecContext(ctx, `DELETE FROM dashboard_items WHERE source_id = ?`, existing.ID)
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
	if err != nil {
		return "", err
	}
	return decryptCredential(password)
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
	lastTest := existing.LastTest
	if !enabled {
		status = "停用"
		if err := resolveStoppedDataSourceNotifications(current, existing, "已停用，平台已停止检测"); err != nil {
			return model.DataSource{}, fmt.Errorf("resolve stopped data source notifications: %w", err)
		}
	} else {
		ok, message := TestDataSourceConnection(existing)
		lastTest = time.Now().Format("2006-01-02 15:04")
		if ok {
			status = "健康"
			resolveDataSourceHealthNotification(current, existing)
		} else {
			status = "异常"
			upsertDataSourceHealthNotification(current, existing, message)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = current.ExecContext(ctx, `UPDATE data_sources SET enabled = ?, status = ?, last_test = ? WHERE id = ?`, enabled, status, lastTest, id)
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
	if ds.Host == "" || (ds.Port == "" && !strings.EqualFold(ds.Type, "hadoop") && !strings.EqualFold(ds.Type, "ambari")) {
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
	case strings.EqualFold(ds.Type, "ssh"):
		if strings.TrimSpace(ds.Username) == "" {
			return false, "SSH 用户名不能为空"
		}
		if strings.TrimSpace(ds.Password) == "" {
			return false, "SSH 密码不能为空"
		}
		if _, err := collectSSHDataSourceMetrics(ds); err != nil {
			return false, fmt.Sprintf("SSH 连接失败：%s", err.Error())
		}
		return true, "SSH 连接测试成功"
	case strings.EqualFold(ds.Type, "hadoop"):
		role, err := TestHadoopDataSource(ds)
		if err != nil {
			return false, err.Error()
		}
		return true, fmt.Sprintf("Hadoop %s Web 接口连接成功", role)
	case strings.EqualFold(ds.Type, "ambari"):
		cluster, err := TestAmbariDataSource(ds)
		if err != nil {
			return false, err.Error()
		}
		return true, fmt.Sprintf("Ambari 集群 %s 接口连接成功", cluster)
	case strings.EqualFold(ds.Type, "redis"):
		if err := TestRedisDataSource(ds); err != nil {
			return false, err.Error()
		}
		return true, "Redis PING 检测成功"
	case strings.EqualFold(ds.Type, "clickhouse"):
		if err := TestClickHouseDataSource(ds); err != nil {
			return false, err.Error()
		}
		return true, "ClickHouse SELECT 1 检测成功"
	case strings.EqualFold(ds.Type, "kafka"):
		if err := TestKafkaDataSource(ds); err != nil {
			return false, err.Error()
		}
		return true, "Kafka Broker 网络检测成功"
	default:
		return false, "目前仅支持 Prometheus、MySQL、SSH、Hadoop 和 Ambari 数据源"
	}
}

func isSupportedDataSourceType(sourceType string) bool {
	return strings.EqualFold(sourceType, "prometheus") || strings.EqualFold(sourceType, "mysql") || strings.EqualFold(sourceType, "ssh") || strings.EqualFold(sourceType, "hadoop") || strings.EqualFold(sourceType, "ambari") || strings.EqualFold(sourceType, "redis") || strings.EqualFold(sourceType, "clickhouse") || strings.EqualFold(sourceType, "kafka")
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
	for {
		checkAllDataSourceConnections()
		setting := GetRefreshSettings("datasource")
		delay := time.Duration(setting.Value) * time.Minute
		switch setting.Unit {
		case "s":
			delay = time.Duration(setting.Value) * time.Second
		case "h":
			delay = time.Duration(setting.Value) * time.Hour
		}
		if delay < time.Second {
			delay = 2 * time.Minute
		}
		time.Sleep(delay)
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

	rows, err := current.Query(`SELECT id, name, type, host, port, COALESCE(username, ''), COALESCE(password, ''), COALESCE(database_name, ''), COALESCE(options_json, '{}'), status FROM data_sources WHERE enabled = 1`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var ds model.DataSource
		var previousStatus string
		var optionsRaw string
		if err := rows.Scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Password, &ds.Database, &optionsRaw, &previousStatus); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(optionsRaw), &ds.Options)
		password, decryptErr := decryptCredential(ds.Password)
		if decryptErr != nil {
			continue
		}
		ds.Password = password
		ok, message := TestDataSourceConnection(ds)
		status := "异常"
		if ok {
			status = "健康"
		}
		_, _ = current.Exec(`UPDATE data_sources SET status = ?, last_test = ? WHERE id = ?`, status, time.Now().Format("2006-01-02 15:04"), ds.ID)
		if !ok && (previousStatus != status || !hasActiveDataSourceHealthNotification(current, ds.ID)) {
			upsertDataSourceHealthNotification(current, ds, message)
		} else if ok && previousStatus != status {
			resolveDataSourceHealthNotification(current, ds)
		}
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
		COALESCE(threshold, ''), time_window, frequency, COALESCE(remark, ''), last_run, last_evaluated_at, COALESCE(result_details, ''), status FROM collection_rules ORDER BY created_at DESC`)
	if err != nil {
		return []model.CollectionRule{}
	}
	defer rows.Close()
	items := []model.CollectionRule{}
	for rows.Next() {
		var rule model.CollectionRule
		var lastEvaluatedAt sql.NullTime
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Source, &rule.Database, &rule.Table, &rule.Field, &rule.Condition, &rule.Threshold, &rule.TimeWindow, &rule.Frequency, &rule.Remark, &rule.LastRun, &lastEvaluatedAt, &rule.ResultDetails, &rule.Status); err != nil {
			continue
		}
		if lastEvaluatedAt.Valid {
			value := lastEvaluatedAt.Time
			rule.LastEvaluatedAt = &value
		}
		items = append(items, rule)
	}
	return items
}

func AddCollectionRule(rule model.CollectionRule) (model.CollectionRule, error) {
	current := currentStore()
	rule = normalizeCollectionRule(rule)
	if err := validateCollectionRule(rule); err != nil {
		return model.CollectionRule{}, err
	}
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
		(id, name, source, database_name, table_name, field_name, condition_text, threshold, time_window, frequency, remark, last_run, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.Name, rule.Source, rule.Database, rule.Table, rule.Field, rule.Condition, rule.Threshold, rule.TimeWindow, rule.Frequency, rule.Remark, rule.LastRun, rule.Status)
	return rule, err
}

func UpdateCollectionRule(id string, rule model.CollectionRule) (model.CollectionRule, error) {
	current := currentStore()
	if current == nil {
		return model.CollectionRule{}, errors.New("collection rule store is not initialized")
	}
	rule.ID = id
	rule = normalizeCollectionRule(rule)
	if err := validateCollectionRule(rule); err != nil {
		return model.CollectionRule{}, err
	}
	if err := validateCollectionRuleSourceEnabled(rule); err != nil {
		return model.CollectionRule{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := current.ExecContext(ctx, `UPDATE collection_rules
		SET name = ?, source = ?, database_name = ?, table_name = ?, field_name = ?, condition_text = ?,
			threshold = ?, time_window = ?, frequency = ?, remark = ?, status = ?, last_evaluated_at = NULL
		WHERE id = ?`,
		rule.Name, rule.Source, rule.Database, rule.Table, rule.Field, rule.Condition, rule.Threshold, rule.TimeWindow, rule.Frequency, rule.Remark, rule.Status, id)
	if err != nil {
		return model.CollectionRule{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		var exists int
		if err := current.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection_rules WHERE id = ?`, id).Scan(&exists); err != nil {
			return model.CollectionRule{}, err
		}
		if exists == 0 {
			return model.CollectionRule{}, errors.New("告警规则不存在或已删除")
		}
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

func ListAlertNotifications(status string, unread string, start string, end string, limit int) []model.AlertNotification {
	current := currentStore()
	if current == nil {
		return []model.AlertNotification{}
	}
	limit = normalizeLimit(limit, 100)
	conditions := []string{"1 = 1"}
	args := []any{}
	status = strings.TrimSpace(status)
	if status == "alerts" {
		conditions = append(conditions, "status IN ('active', 'alert')")
	} else if status == "active" {
		conditions = append(conditions, "status = 'active'")
	} else if status != "" && status != "all" {
		conditions = append(conditions, "status = ?")
		args = append(args, status)
	}
	if unread == "1" || strings.EqualFold(unread, "true") {
		conditions = append(conditions, "unread = 1 AND muted = 0")
	}
	if startDate, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(start), time.Local); err == nil && strings.TrimSpace(start) != "" {
		endDate, endErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(end), time.Local)
		if endErr != nil || strings.TrimSpace(end) == "" || endDate.Before(startDate) {
			endDate = startDate
		}
		queryEnd := endDate.AddDate(0, 0, 1)
		// An active alert represents one continuous incident. It must appear in
		// every queried day after its first trigger, while a recovered event is
		// only a notification on the day it was recovered.
		conditions = append(conditions, `(
			(status IN ('active', 'alert') AND first_seen_at < ? AND (status = 'active' OR last_seen_at >= ?))
			OR (status = 'resolved' AND last_seen_at >= ? AND last_seen_at < ?)
		)`)
		args = append(args, queryEnd, startDate, startDate, queryEnd)
	}
	args = append(args, limit)
	rows, err := current.Query(`SELECT id, rule_id, rule_name, source, database_name, table_name, field_name,
		severity, status, message, unread, muted, first_seen_at, last_seen_at, resolved_at
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
			&item.Severity, &item.Status, &item.Message, &item.Unread, &item.Muted, &item.FirstSeenAt, &item.LastSeenAt, &resolvedAt); err != nil {
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
	_ = current.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notifications WHERE unread = 1 AND muted = 0`).Scan(&count)
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

func SetAlertNotificationMuted(id string, muted bool) error {
	current := currentStore()
	if current == nil {
		return errors.New("notification store is not initialized")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("notification id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := current.ExecContext(ctx, `UPDATE alert_notifications
		SET muted = ?, unread = CASE WHEN ? THEN 0 ELSE unread END WHERE id = ?`, muted, muted, id)
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
		source varchar(512) NOT NULL,
		database_name varchar(120) NOT NULL DEFAULT '',
		table_name varchar(120) NOT NULL DEFAULT '',
		field_name varchar(120) NOT NULL DEFAULT '',
		condition_text varchar(120) NOT NULL,
		threshold varchar(80) NULL,
		time_window varchar(80) NOT NULL DEFAULT '5分钟',
		frequency varchar(32) NOT NULL DEFAULT '1分钟',
		remark varchar(255) NOT NULL DEFAULT '',
		alert_started_at datetime NULL,
		last_evaluated_at datetime NULL,
		last_run varchar(64) NOT NULL DEFAULT '待执行',
		result_details text NULL,
		status varchar(32) NOT NULL DEFAULT '启用',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}
	_, _ = appDB.Exec(`ALTER TABLE collection_rules ADD COLUMN frequency varchar(32) NOT NULL DEFAULT '1分钟' AFTER time_window`)
	_, _ = appDB.Exec(`ALTER TABLE collection_rules ADD COLUMN remark varchar(255) NOT NULL DEFAULT '' AFTER frequency`)
	_, _ = appDB.Exec(`ALTER TABLE collection_rules ADD COLUMN alert_started_at datetime NULL AFTER last_evaluated_at`)
	_, _ = appDB.Exec(`ALTER TABLE collection_rules ADD COLUMN last_evaluated_at datetime NULL AFTER frequency`)
	_, _ = appDB.Exec(`ALTER TABLE collection_rules MODIFY alert_started_at datetime NULL`)
	_, _ = appDB.Exec(`ALTER TABLE collection_rules MODIFY last_evaluated_at datetime NULL`)
	_, _ = appDB.Exec(`ALTER TABLE collection_rules ADD COLUMN result_details text NULL AFTER last_run`)
	_, _ = appDB.Exec(`ALTER TABLE collection_rules MODIFY source varchar(512) NOT NULL`)
	_, _ = appDB.Exec(`ALTER TABLE collection_rules MODIFY last_run varchar(512) NOT NULL DEFAULT '待执行'`)
	// Old releases could leave a stale evaluation timestamp on a rule whose
	// result still says "待执行". Do not clear completed rules here: doing so
	// would cause every rule to run immediately after a service restart.
	_, _ = appDB.Exec(`UPDATE collection_rules SET last_evaluated_at = NULL WHERE last_run = '待执行'`)
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
		muted tinyint(1) NOT NULL DEFAULT 0,
		first_seen_at datetime NOT NULL,
		last_seen_at datetime NOT NULL,
		resolved_at datetime NULL,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		INDEX idx_alert_notifications_status_time (status, last_seen_at),
		INDEX idx_alert_notifications_unread (unread)
	)`)
	if err != nil {
		return err
	}
	_, _ = appDB.Exec(`ALTER TABLE alert_notifications ADD COLUMN muted tinyint(1) NOT NULL DEFAULT 0 AFTER unread`)
	_, _ = appDB.Exec(`UPDATE alert_notifications SET unread = 0 WHERE muted = 1 AND unread = 1`)
	if err := migrateAlertNotificationTimes(appDB); err != nil {
		return err
	}
	if err := backfillLegacyDataSourceHealthAlerts(appDB); err != nil {
		return err
	}
	if err := backfillResolvedAlertHistory(appDB); err != nil {
		return err
	}
	return deduplicateActiveAlertNotifications(appDB)
}

// A single rule represents one ongoing incident. Older releases could leave
// multiple active rows for it when notification IDs changed across upgrades.
func deduplicateActiveAlertNotifications(appDB *sql.DB) error {
	rows, err := appDB.Query(`SELECT rule_id FROM alert_notifications
		WHERE status = 'active' GROUP BY rule_id HAVING COUNT(*) > 1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	ruleIDs := make([]string, 0)
	for rows.Next() {
		var ruleID string
		if err := rows.Scan(&ruleID); err == nil {
			ruleIDs = append(ruleIDs, ruleID)
		}
	}
	for _, ruleID := range ruleIDs {
		var keepID string
		var firstSeen time.Time
		err := appDB.QueryRow(`SELECT id, first_seen_at FROM alert_notifications
			WHERE rule_id = ? AND status = 'active'
			ORDER BY last_seen_at DESC, first_seen_at DESC, id DESC LIMIT 1`, ruleID).Scan(&keepID, &firstSeen)
		if err != nil {
			return err
		}
		var earliest time.Time
		if err := appDB.QueryRow(`SELECT MIN(first_seen_at) FROM alert_notifications
			WHERE rule_id = ? AND status = 'active'`, ruleID).Scan(&earliest); err != nil {
			return err
		}
		if earliest.Before(firstSeen) {
			if _, err := appDB.Exec(`UPDATE alert_notifications SET first_seen_at = ? WHERE id = ?`, earliest, keepID); err != nil {
				return err
			}
		}
		if _, err := appDB.Exec(`DELETE FROM alert_notifications WHERE rule_id = ? AND status = 'active' AND id <> ?`, ruleID, keepID); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Older data-source health notifications were overwritten in place when they
// recovered. Preserve a clearly marked historical alert beside that recovery.
func backfillLegacyDataSourceHealthAlerts(appDB *sql.DB) error {
	_, err := appDB.Exec(`INSERT INTO alert_notifications
		(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, muted, first_seen_at, last_seen_at)
		SELECT CONCAT(n.id, '-legacy-alert'), n.rule_id, n.rule_name, n.source, n.database_name, n.table_name, n.field_name,
			n.severity, 'alert', CONCAT('数据源 ', REPLACE(n.rule_name, ' 数据源异常', ''), ' 曾发生连通性异常（历史恢复前记录）'),
			0, n.muted, n.first_seen_at, n.first_seen_at
		FROM alert_notifications n
		WHERE n.id LIKE 'datasource-health-%'
			AND n.id NOT LIKE '%-alert-%'
			AND n.id NOT LIKE '%-recovered-%'
			AND n.status = 'resolved'
			AND NOT EXISTS (
				SELECT 1 FROM alert_notifications history
				WHERE history.rule_id = n.rule_id AND history.status = 'alert'
			)`)
	return err
}

// Before alert and recovery were represented as separate events, a recovery
// could overwrite its original alert row. Restore that missing history once.
func backfillResolvedAlertHistory(appDB *sql.DB) error {
	_, err := appDB.Exec(`INSERT INTO alert_notifications
		(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, muted, first_seen_at, last_seen_at)
		SELECT CONCAT(n.id, '-legacy-alert-history'), n.rule_id, n.rule_name, n.source, n.database_name, n.table_name, n.field_name,
			n.severity, 'alert', REPLACE(n.message, '已恢复：', ''), 0, n.muted, n.first_seen_at, n.first_seen_at
		FROM alert_notifications n
		WHERE n.status = 'resolved'
			AND NOT EXISTS (
				SELECT 1 FROM alert_notifications history
				WHERE history.rule_id = n.rule_id
					AND history.status = 'alert'
					AND history.first_seen_at = n.first_seen_at
			)`)
	return err
}

func migrateAlertNotificationTimes(appDB *sql.DB) error {
	var dataType string
	err := appDB.QueryRow(`SELECT DATA_TYPE FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'alert_notifications' AND COLUMN_NAME = 'first_seen_at'`).Scan(&dataType)
	if err != nil {
		return err
	}
	if strings.EqualFold(dataType, "timestamp") {
		// Earlier releases wrote notifications with the database's UTC NOW().
		// Shift those legacy values once before making the columns timezone-neutral.
		if _, err := appDB.Exec(`UPDATE alert_notifications SET first_seen_at = DATE_ADD(first_seen_at, INTERVAL 8 HOUR),
			last_seen_at = DATE_ADD(last_seen_at, INTERVAL 8 HOUR),
			resolved_at = CASE WHEN resolved_at IS NULL THEN NULL ELSE DATE_ADD(resolved_at, INTERVAL 8 HOUR) END`); err != nil {
			return err
		}
		for _, column := range []string{"first_seen_at", "last_seen_at", "resolved_at"} {
			nullable := "NOT NULL"
			if column == "resolved_at" {
				nullable = "NULL"
			}
			if _, err := appDB.Exec(`ALTER TABLE alert_notifications MODIFY ` + column + ` datetime ` + nullable); err != nil {
				return err
			}
		}
	}
	// Legacy notification IDs contain their original calendar day. The initial
	// UTC repair could cross midnight for those rows; restore them to that day.
	_, err = appDB.Exec(`UPDATE alert_notifications
		SET first_seen_at = DATE_SUB(first_seen_at, INTERVAL 8 HOUR),
			last_seen_at = DATE_SUB(last_seen_at, INTERVAL 8 HOUR),
			resolved_at = CASE WHEN resolved_at IS NULL THEN NULL ELSE DATE_SUB(resolved_at, INTERVAL 8 HOUR) END
		WHERE id REGEXP '-[0-9]{8}$'
			AND DATE(first_seen_at) <> STR_TO_DATE(RIGHT(id, 8), '%Y%m%d')`)
	if err != nil {
		return err
	}
	return nil
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
			ticker := time.NewTicker(time.Second)
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
	rows, err := current.Query(`SELECT id, name, source, database_name, table_name, field_name, condition_text, COALESCE(threshold, ''), time_window, frequency, last_evaluated_at,
		CASE WHEN source = 'custom-probe' THEN NULL ELSE alert_started_at END
		FROM collection_rules WHERE status = '启用'`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var rule model.CollectionRule
		var lastEvaluatedAt sql.NullTime
		var alertStartedAt sql.NullTime
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Source, &rule.Database, &rule.Table, &rule.Field, &rule.Condition, &rule.Threshold, &rule.TimeWindow, &rule.Frequency, &lastEvaluatedAt, &alertStartedAt); err != nil {
			continue
		}
		if !shouldEvaluateCollectionRule(lastEvaluatedAt, time.Now(), rule.Frequency) {
			continue
		}
		var startedAt *time.Time
		if alertStartedAt.Valid {
			startedAt = &alertStartedAt.Time
		}
		lastRun, thresholdMatched, resultDetails := evaluateCollectionRule(rule, startedAt)
		evaluatedAt := time.Now()
		_, _ = current.Exec(`UPDATE collection_rules SET last_run = ?, result_details = ?, last_evaluated_at = ?, alert_started_at = CASE WHEN ? THEN COALESCE(alert_started_at, ?) ELSE NULL END WHERE id = ?`, lastRun, resultDetails, evaluatedAt, thresholdMatched, evaluatedAt, rule.ID)
		syncAlertNotification(current, rule, lastRun)
	}
}

func shouldEvaluateCollectionRule(lastEvaluatedAt sql.NullTime, now time.Time, frequency string) bool {
	// A future timestamp caused by a clock correction must not block this rule forever.
	if !lastEvaluatedAt.Valid || lastEvaluatedAt.Time.After(now) {
		return true
	}
	return now.Sub(lastEvaluatedAt.Time) >= collectionRuleFrequency(frequency)
}

func evaluateCollectionRule(rule model.CollectionRule, alertStartedAt *time.Time) (string, bool, string) {
	if isCustomProbeRule(rule) {
		return evaluateCustomProbeRule(rule), false, ""
	}
	if isFileMonitorRule(rule) {
		return evaluateFileMonitorRule(rule)
	}
	if isScriptMonitorRule(rule) {
		return evaluateScriptMonitorRule(rule)
	}
	if isDataMonitorRule(rule) {
		return evaluateDataMonitorRule(rule), false, ""
	}
	lastRun, matched := evaluateMetricThresholdRule(rule, alertStartedAt)
	return lastRun, matched, ""
}

func isCustomProbeRule(rule model.CollectionRule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.Source), "custom-probe")
}

func evaluateMetricThresholdRule(rule model.CollectionRule, alertStartedAt *time.Time) (string, bool) {
	now := time.Now()
	checkedAt := now.Format("15:04")
	ds, err := getRuleDataSourceWithSecret(rule.Source)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：%s", checkedAt, err.Error()), false
	}
	if !strings.EqualFold(ds.Type, "prometheus") {
		return fmt.Sprintf("等待 %s：普通阈值规则评估待接入", checkedAt), false
	}
	data, err := QueryPrometheusDataSourceByID(ds.ID, rule.Table)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：Prometheus 查询失败：%s", checkedAt, err.Error()), false
	}
	value, ok := firstPrometheusVectorValue(data)
	if !ok {
		return fmt.Sprintf("执行失败 %s：Prometheus 查询无数据", checkedAt), false
	}
	threshold, err := strconv.ParseFloat(strings.TrimSpace(rule.Threshold), 64)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：阈值必须是数字", checkedAt), false
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
		timeout := collectionRuleFrequency(rule.TimeWindow)
		if alertStartedAt == nil {
			return fmt.Sprintf("等待 %s：Prometheus 指标达到阈值，持续 %s 后告警", checkedAt, rule.TimeWindow), true
		}
		if time.Since(*alertStartedAt) < timeout {
			return fmt.Sprintf("等待 %s：Prometheus 指标已持续 %s，等待 %s", checkedAt, time.Since(*alertStartedAt).Round(time.Second), rule.TimeWindow), true
		}
		return fmt.Sprintf("告警 %s：Prometheus 指标 %.4f %s %.4f", checkedAt, value, rule.Condition, threshold), true
	}
	return fmt.Sprintf("正常 %s：Prometheus 指标 %.4f 未触发阈值", checkedAt, value), false
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
	case "udp", "udp端口":
		return evaluateUDPProbe(target, timeout, checkedAt)
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

func evaluateUDPProbe(target string, timeout time.Duration, checkedAt string) string {
	if _, _, err := net.SplitHostPort(target); err != nil {
		return fmt.Sprintf("执行失败 %s：UDP 探测目标必须是 host:port", checkedAt)
	}
	start := time.Now()
	conn, err := net.DialTimeout("udp", target, timeout)
	if err != nil {
		return fmt.Sprintf("告警 %s：UDP 探测失败：%s", checkedAt, err.Error())
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0}); err != nil {
		return fmt.Sprintf("告警 %s：UDP 写入失败：%s", checkedAt, err.Error())
	}
	return fmt.Sprintf("正常 %s：UDP 可达，耗时 %dms", checkedAt, time.Since(start).Milliseconds())
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

func collectionRuleFrequency(value string) time.Duration {
	text := strings.TrimSpace(strings.NewReplacer("小时", "h", "时", "h", "秒", "s", "分钟", "m", "分", "m").Replace(value))
	duration, err := time.ParseDuration(text)
	if err != nil || duration < time.Second || duration > 24*time.Hour {
		return time.Minute
	}
	return duration
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now()
	var id string
	err := appDB.QueryRowContext(ctx, `SELECT id FROM alert_notifications WHERE rule_id = ? AND status = 'active' ORDER BY first_seen_at DESC LIMIT 1`, rule.ID).Scan(&id)
	if err == nil {
		_, _ = appDB.ExecContext(ctx, `UPDATE alert_notifications
			SET rule_name = ?, source = ?, database_name = ?, table_name = ?, field_name = ?, severity = ?, message = ?, unread = IF(muted = 1, 0, 1), last_seen_at = ?
			WHERE id = ?`, rule.Name, rule.Source, rule.Database, rule.Table, rule.Field, severity, message, now, id)
		return
	}
	id = fmt.Sprintf("%s-alert-%d", rule.ID, time.Now().UnixNano())
	_, _ = appDB.ExecContext(ctx, `INSERT INTO alert_notifications
		(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		id, rule.ID, rule.Name, rule.Source, rule.Database, rule.Table, rule.Field, severity, status, message, now, now)
}

func resolveAlertNotification(appDB *sql.DB, ruleID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := appDB.QueryContext(ctx, `SELECT id, rule_name, source, database_name, table_name, field_name, severity, message, first_seen_at
		FROM alert_notifications WHERE rule_id = ? AND status = 'active'`, ruleID)
	if err != nil {
		return
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var id, ruleName, source, databaseName, tableName, fieldName, severity, message string
		var firstSeenAt time.Time
		if err := rows.Scan(&id, &ruleName, &source, &databaseName, &tableName, &fieldName, &severity, &message, &firstSeenAt); err != nil {
			continue
		}
		_, _ = appDB.ExecContext(ctx, `UPDATE alert_notifications SET status = 'alert', unread = IF(muted = 1, 0, 1) WHERE id = ?`, id)
		_, _ = appDB.ExecContext(ctx, `INSERT INTO alert_notifications
			(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, first_seen_at, last_seen_at, resolved_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'resolved', ?, 1, ?, ?, ?)`,
			fmt.Sprintf("%s-recovered-%d", ruleID, time.Now().UnixNano()), ruleID, ruleName, source, databaseName, tableName, fieldName, severity, "已恢复："+message, firstSeenAt, now, now)
	}
}

func dataSourceHealthNotificationID(sourceID string) string {
	return "datasource-health-" + sourceID
}

// Deleting a source must stop its active notifications without erasing the
// audit trail. A recovery event makes the reason explicit and prevents the
// notification center from retaining a stale active alert indefinitely.
func resolveDeletedDataSourceNotifications(appDB *sql.DB, ds model.DataSource) {
	_ = resolveStoppedDataSourceNotifications(appDB, ds, "已删除，平台已停止检测")
}

// Stopping a source ends every active notification produced by that source.
// The historical alert remains available and a single unread recovery event
// explains why the platform will no longer evaluate it.
func resolveStoppedDataSourceNotifications(appDB *sql.DB, ds model.DataSource, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := appDB.QueryContext(ctx, `SELECT id, rule_id, rule_name, source, database_name, table_name, field_name, severity, first_seen_at
		FROM alert_notifications
		WHERE status = 'active' AND (source = ? OR FIND_IN_SET(?, source) > 0)`, ds.ID, ds.ID)
	if err != nil {
		return err
	}
	type activeNotification struct {
		id, ruleID, ruleName, source, databaseName, tableName, fieldName, severity string
		firstSeenAt                                                                time.Time
	}
	var activeNotifications []activeNotification
	for rows.Next() {
		var notification activeNotification
		if err := rows.Scan(&notification.id, &notification.ruleID, &notification.ruleName, &notification.source, &notification.databaseName, &notification.tableName, &notification.fieldName, &notification.severity, &notification.firstSeenAt); err != nil {
			continue
		}
		activeNotifications = append(activeNotifications, notification)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	now := time.Now()
	for _, notification := range activeNotifications {
		// The prior event remains as history. It has already been notified, so
		// only the newly created recovery is left unread.
		if _, err := appDB.ExecContext(ctx, `UPDATE alert_notifications SET status = 'alert', unread = 0, last_seen_at = ? WHERE id = ?`, now, notification.id); err != nil {
			return err
		}
		if _, err := appDB.ExecContext(ctx, `INSERT INTO alert_notifications
			(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, first_seen_at, last_seen_at, resolved_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'resolved', ?, 1, ?, ?, ?)`,
			fmt.Sprintf("source-stop-%d", now.UnixNano()), notification.ruleID, notification.ruleName, notification.source, notification.databaseName, notification.tableName, notification.fieldName, notification.severity,
			fmt.Sprintf("数据源 %s %s", ds.Name, reason), notification.firstSeenAt, now, now); err != nil {
			return err
		}
	}
	return rows.Err()
}

func hasActiveDataSourceHealthNotification(appDB *sql.DB, sourceID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var count int
	_ = appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notifications WHERE rule_id = ? AND status = 'active'`, "datasource:"+sourceID).Scan(&count)
	return count > 0
}

func upsertDataSourceHealthNotification(appDB *sql.DB, ds model.DataSource, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now()
	message := fmt.Sprintf("数据源 %s 连通性异常", ds.Name)
	if strings.TrimSpace(detail) != "" {
		message += "：" + strings.TrimSpace(detail)
	}
	var id string
	err := appDB.QueryRowContext(ctx, `SELECT id FROM alert_notifications WHERE rule_id = ? AND status = 'active' ORDER BY first_seen_at DESC LIMIT 1`, "datasource:"+ds.ID).Scan(&id)
	if err == nil {
		_, _ = appDB.ExecContext(ctx, `UPDATE alert_notifications
			SET rule_name = ?, source = ?, database_name = ?, table_name = ?, severity = 'critical',
				message = ?, unread = IF(muted = 1, 0, 1), last_seen_at = ? WHERE id = ?`,
			ds.Name+" 数据源异常", ds.ID, ds.Type, ds.Host+":"+ds.Port, message, now, id)
		return
	}
	_, _ = appDB.ExecContext(ctx, `INSERT INTO alert_notifications
		(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, '', 'critical', 'active', ?, 1, ?, ?)`,
		fmt.Sprintf("%s-alert-%d", dataSourceHealthNotificationID(ds.ID), now.UnixNano()), "datasource:"+ds.ID, ds.Name+" 数据源异常", ds.ID, ds.Type, ds.Host+":"+ds.Port, message, now, now)
}

func resolveDataSourceHealthNotification(appDB *sql.DB, ds model.DataSource) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := appDB.QueryContext(ctx, `SELECT id, rule_name, source, database_name, table_name, field_name, severity, message, first_seen_at
		FROM alert_notifications WHERE rule_id = ? AND status = 'active'`, "datasource:"+ds.ID)
	if err != nil {
		return
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var id, ruleName, source, databaseName, tableName, fieldName, severity, message string
		var firstSeenAt time.Time
		if err := rows.Scan(&id, &ruleName, &source, &databaseName, &tableName, &fieldName, &severity, &message, &firstSeenAt); err != nil {
			continue
		}
		_, _ = appDB.ExecContext(ctx, `UPDATE alert_notifications SET status = 'alert', unread = IF(muted = 1, 0, 1) WHERE id = ?`, id)
		_, _ = appDB.ExecContext(ctx, `INSERT INTO alert_notifications
			(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, first_seen_at, last_seen_at, resolved_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'resolved', ?, 1, ?, ?, ?)`,
			fmt.Sprintf("datasource-health-%s-recovered-%d", ds.ID, now.UnixNano()), "datasource:"+ds.ID, ruleName, source, databaseName, tableName, fieldName, severity,
			fmt.Sprintf("已恢复：数据源 %s 连通性正常", ds.Name), firstSeenAt, now, now)
	}
}

func isDataMonitorRule(rule model.CollectionRule) bool {
	return rule.Condition == "当天有数据" || rule.Condition == "今天有数据"
}

func isFileMonitorRule(rule model.CollectionRule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.Database), "file-monitor")
}

func isScriptMonitorRule(rule model.CollectionRule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.Database), "script-monitor")
}

func collectionRuleSourceIDs(source string) []string {
	seen := map[string]bool{}
	ids := make([]string, 0)
	for _, value := range strings.Split(source, ",") {
		id := strings.TrimSpace(value)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func getRuleSSHDatasources(source string) ([]model.DataSource, error) {
	ids := collectionRuleSourceIDs(source)
	if len(ids) == 0 {
		return nil, errors.New("至少选择一个 SSH 数据源")
	}
	items := make([]model.DataSource, 0, len(ids))
	for _, id := range ids {
		ds, err := getRuleDataSourceWithSecret(id)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(ds.Type, "ssh") {
			return nil, errors.New("数据源类型不是 SSH")
		}
		items = append(items, ds)
	}
	return items, nil
}

func evaluateFileMonitorRule(rule model.CollectionRule) (string, bool, string) {
	now := time.Now()
	checkedAt := now.Format("15:04")
	deadline := todayDataRuleDeadline(rule.TimeWindow, now)
	sources, err := getRuleSSHDatasources(rule.Source)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：%s", checkedAt, err.Error()), false, err.Error()
	}
	pattern, err := regexp.Compile(rule.Field)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：文件名正则无效：%s", checkedAt, err.Error()), false, err.Error()
	}
	// The remote command is fixed. Only the user-selected directory is quoted;
	// filtering is performed locally with Go's RE2 engine.
	command := "LC_ALL=C find " + shellQuote(rule.Table) + " -maxdepth 1 -type f -daystart -mtime 0 -printf '%f\\n' | head -n 200"
	details := "目录：" + rule.Table + "\n文件名正则：" + rule.Field + "\n"
	missing := make([]string, 0)
	for _, ds := range sources {
		output, commandErr := executeSSHCommand(ds, command)
		details += "\n[" + ds.Name + "]\n"
		if commandErr != nil {
			details += "执行失败：" + commandErr.Error() + "\n"
			return fmt.Sprintf("执行失败 %s：%s 文件检测失败：%s", checkedAt, ds.Name, commandErr.Error()), false, details
		}
		matches := make([]string, 0)
		for _, fileName := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
			fileName = strings.TrimSuffix(fileName, "\r")
			if fileName == "" {
				continue
			}
			details += "- " + fileName + "\n"
			if pattern.MatchString(fileName) {
				matches = append(matches, fileName)
			}
		}
		if len(matches) == 0 {
			missing = append(missing, ds.Name)
		} else {
			details += "命中：" + strings.Join(matches, ", ") + "\n"
		}
	}
	if len(missing) == 0 {
		return fmt.Sprintf("正常 %s：结果详情", checkedAt), false, details
	}
	if now.Before(deadline) {
		return fmt.Sprintf("等待 %s：%s 未生成匹配文件，%s 后仍未生成再告警", checkedAt, strings.Join(missing, "、"), deadline.Format("15:04")), false, details
	}
	return fmt.Sprintf("告警 %s：%s 当天未生成匹配文件（目录：%s）", checkedAt, strings.Join(missing, "、"), rule.Table), false, details
}

func evaluateScriptMonitorRule(rule model.CollectionRule) (string, bool, string) {
	now := time.Now()
	checkedAt := now.Format("15:04")
	sources, err := getRuleSSHDatasources(rule.Source)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：%s", checkedAt, err.Error()), false, err.Error()
	}
	expectedCode, _ := strconv.Atoi(rule.Threshold)
	configuredDirectory := strings.TrimSpace(rule.Field)
	if configuredDirectory == "" {
		configuredDirectory = "SSH 默认目录"
	}
	details := "检测时间：" + now.Format("2006-01-02 15:04:05") + "\n判断方式：退出码等于 " + strconv.Itoa(expectedCode) + "\n"
	failed := make([]string, 0)
	for _, ds := range sources {
		command := scriptMonitorCommand(rule.Table, rule.Field)
		output, exitCode, commandErr := executeSSHCommandWithExitStatus(ds, command)
		actualDirectory, output := scriptMonitorOutputDirectory(output, configuredDirectory)
		details += "\n========== " + ds.Name + " ==========\n"
		details += "执行目录：" + actualDirectory + "\n执行命令：\n" + rule.Table + "\n退出码："
		if commandErr != nil {
			details += "未获取\n执行失败：" + commandErr.Error() + "\n"
			return fmt.Sprintf("执行失败 %s：%s 脚本执行失败：%s", checkedAt, ds.Name, commandErr.Error()), false, details
		}
		details += strconv.Itoa(exitCode) + "\n执行结果：\n" + truncateRuleResult(output, 4000) + "\n"
		if exitCode != expectedCode {
			failed = append(failed, ds.Name+"("+strconv.Itoa(exitCode)+")")
		}
	}
	if len(failed) == 0 {
		return fmt.Sprintf("正常 %s：所有节点脚本退出码符合预期", checkedAt), false, details
	}
	return fmt.Sprintf("告警 %s：%s 脚本退出码不符合预期 %d", checkedAt, strings.Join(failed, "、"), expectedCode), false, details
}

const scriptMonitorDirectoryMarker = "__OPSGUARD_WORKDIR__="

func scriptMonitorCommand(script string, directory string) string {
	var command strings.Builder
	if directory = strings.TrimSpace(directory); directory != "" {
		command.WriteString("cd -- ")
		command.WriteString(shellQuote(directory))
		command.WriteString(" || exit 125\n")
	}
	command.WriteString("printf '")
	command.WriteString(scriptMonitorDirectoryMarker)
	command.WriteString("%s\\n' \"$PWD\"\n")
	command.WriteString(script)
	return command.String()
}

func scriptMonitorOutputDirectory(output string, fallback string) (string, string) {
	if strings.HasPrefix(output, scriptMonitorDirectoryMarker) {
		line, remainder, found := strings.Cut(output, "\n")
		if found {
			if directory := strings.TrimSpace(strings.TrimPrefix(line, scriptMonitorDirectoryMarker)); directory != "" {
				return directory, remainder
			}
		}
	}
	return fallback, output
}

func truncateRuleResult(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "（无输出）"
	}
	if len(value) > limit {
		return value[:limit] + "\n...（输出已截断）"
	}
	return value
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\\"'\\\"'") + "'"
}

func evaluateDataMonitorRule(rule model.CollectionRule) string {
	now := time.Now()
	checkedAt := now.Format("15:04")
	deadline := todayDataRuleDeadline(rule.TimeWindow, now)
	ds, err := getRuleDataSourceWithSecret(rule.Source)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：%s", checkedAt, err.Error())
	}
	if !strings.EqualFold(ds.Type, "mysql") {
		return fmt.Sprintf("执行失败 %s：数据监控规则仅支持 MySQL 数据源", checkedAt)
	}
	if !validSQLIdentifier(rule.Database) || !validSQLIdentifier(rule.Table) {
		return fmt.Sprintf("执行失败 %s：库名或表名不合法", checkedAt)
	}
	if !validSQLIdentifier(rule.Field) {
		return fmt.Sprintf("执行失败 %s：数据监控规则必须设置有效的日期字段", checkedAt)
	}
	targetDB, err := openMySQLDataSource(ds)
	if err != nil {
		return fmt.Sprintf("执行失败 %s：%s", checkedAt, err.Error())
	}
	defer targetDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	end := start.AddDate(0, 0, 1)
	query := fmt.Sprintf("SELECT COUNT(*) FROM `%s`.`%s` WHERE `%s` >= ? AND `%s` < ?", rule.Database, rule.Table, rule.Field, rule.Field)
	args := []any{start, end}
	var count int64
	if err := targetDB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return fmt.Sprintf("执行失败 %s：查询失败：%s", checkedAt, err.Error())
	}
	if count > 0 {
		return fmt.Sprintf("正常 %s：%s.%s 当天新增 %d 条", checkedAt, rule.Database, rule.Table, count)
	}
	if now.Before(deadline) {
		return fmt.Sprintf("等待 %s：%s.%s 当天暂无新增数据，%s 后仍无数据再告警", checkedAt, rule.Database, rule.Table, deadline.Format("15:04"))
	}
	return fmt.Sprintf("告警 %s：%s.%s 当天暂无新增数据", checkedAt, rule.Database, rule.Table)
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
		FROM data_sources WHERE id = ?`, source).
		Scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Password, &ds.Database, &ds.Remark, &optionsRaw, &ds.Enabled, &ds.Status, &ds.LastTest)
	if err != nil {
		return model.DataSource{}, errors.New("数据源不存在")
	}
	_ = json.Unmarshal([]byte(optionsRaw), &ds.Options)
	password, err := decryptCredential(ds.Password)
	if err != nil {
		return model.DataSource{}, err
	}
	ds.Password = password
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
	rule.Frequency = strings.TrimSpace(rule.Frequency)
	rule.Status = strings.TrimSpace(rule.Status)
	if rule.Condition == "今天有数据" {
		rule.Condition = "当天有数据"
	}
	if rule.Condition == "" {
		rule.Condition = "大于"
	}
	if rule.TimeWindow == "" {
		rule.TimeWindow = "5分钟"
	}
	if rule.Frequency == "" {
		rule.Frequency = "1分钟"
	}
	if rule.LastRun == "" {
		rule.LastRun = "待执行"
	}
	if rule.Status == "" {
		rule.Status = "启用"
	}
	return rule
}

func validateCollectionRule(rule model.CollectionRule) error {
	if rule.Name == "" {
		return errors.New("规则名称不能为空")
	}
	if collectionRuleFrequency(rule.Frequency) <= 0 {
		return errors.New("采集频率格式不正确")
	}
	if isCustomProbeRule(rule) {
		return validateCustomProbeRule(rule)
	}
	if isFileMonitorRule(rule) {
		if rule.Source == "" || rule.Table == "" || rule.Field == "" {
			return errors.New("文件检测规则必须选择 SSH 数据源、路径和文件名正则")
		}
		if !strings.HasPrefix(rule.Table, "/") || strings.ContainsAny(rule.Table, "\r\n\x00") {
			return errors.New("文件检测路径必须是有效的 Linux 绝对路径")
		}
		if _, err := regexp.Compile(rule.Field); err != nil {
			return fmt.Errorf("文件名正则无效：%w", err)
		}
		if _, err := time.Parse("15:04", rule.TimeWindow); err != nil {
			return errors.New("文件检测截止时间必须为 HH:MM")
		}
		return nil
	}
	if isScriptMonitorRule(rule) {
		if len(collectionRuleSourceIDs(rule.Source)) == 0 {
			return errors.New("脚本检测至少选择一个 SSH 数据源")
		}
		if strings.TrimSpace(rule.Table) == "" || len(rule.Table) > 8000 || strings.Contains(rule.Table, "\x00") {
			return errors.New("检测脚本不能为空，且长度不能超过 8000 个字符")
		}
		if directory := strings.TrimSpace(rule.Field); directory != "" && (!strings.HasPrefix(directory, "/") || strings.ContainsAny(directory, "\r\n\x00")) {
			return errors.New("脚本执行目录必须是有效的 Linux 绝对路径")
		}
		if rule.Condition != "退出码等于" {
			return errors.New("脚本检测暂只支持退出码等于")
		}
		exitCode, err := strconv.Atoi(rule.Threshold)
		if err != nil || exitCode < 0 || exitCode > 255 {
			return errors.New("预期退出码必须是 0 到 255 之间的整数")
		}
		return nil
	}
	if isDataMonitorRule(rule) {
		if rule.Source == "" || rule.Database == "" || rule.Table == "" || rule.Field == "" {
			return errors.New("数据监控规则必须选择数据源、数据库、表和日期字段")
		}
		return nil
	}
	if rule.Source == "" {
		return errors.New("Prometheus 规则必须选择数据源")
	}
	if rule.Table == "" {
		return errors.New("Prometheus 规则必须填写 PromQL")
	}
	if rule.Condition != "大于" && rule.Condition != "小于" && rule.Condition != "等于" {
		return errors.New("Prometheus 规则判断方式不支持")
	}
	if _, err := strconv.ParseFloat(rule.Threshold, 64); err != nil {
		return errors.New("Prometheus 规则阈值必须是数字")
	}
	return nil
}

func validateCustomProbeRule(rule model.CollectionRule) error {
	probeType := strings.ToLower(rule.Database)
	target := rule.Table
	if target == "" {
		return errors.New("探测目标不能为空")
	}
	switch probeType {
	case "http", "https", "http页面":
		parsedTarget := target
		if !strings.HasPrefix(parsedTarget, "http://") && !strings.HasPrefix(parsedTarget, "https://") {
			parsedTarget = "http://" + parsedTarget
		}
		parsed, err := url.ParseRequestURI(parsedTarget)
		if err != nil || parsed.Host == "" {
			return errors.New("HTTP 探测目标不是有效地址")
		}
		if rule.Condition != "状态码小于400" && rule.Condition != "状态码等于" && rule.Condition != "页面包含" {
			return errors.New("HTTP 探测判断方式不支持")
		}
		if rule.Condition == "状态码等于" {
			statusCode, err := strconv.Atoi(rule.Threshold)
			if err != nil || statusCode < 100 || statusCode > 599 {
				return errors.New("HTTP 状态码必须是 100 到 599 之间的数字")
			}
		}
		if rule.Condition == "页面包含" && rule.Threshold == "" {
			return errors.New("页面包含规则必须填写期望内容")
		}
		return nil
	case "tcp", "tcp端口", "udp", "udp端口":
		_, port, err := net.SplitHostPort(target)
		if err != nil {
			if probeType == "udp" || probeType == "udp端口" {
				return errors.New("UDP 探测目标必须是 host:port")
			}
			return errors.New("TCP 探测目标必须是 host:port")
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			if probeType == "udp" || probeType == "udp端口" {
				return errors.New("UDP 探测端口必须是 1 到 65535 之间的数字")
			}
			return errors.New("TCP 探测端口必须是 1 到 65535 之间的数字")
		}
		return nil
	default:
		return errors.New("仅支持 HTTP、TCP 或 UDP 探测规则")
	}
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
	sourceIDs := collectionRuleSourceIDs(rule.Source)
	if len(sourceIDs) == 0 {
		return errors.New("启用告警规则前，请先选择有效的数据源")
	}
	for _, sourceID := range sourceIDs {
		ds, err := GetDataSourceByID(sourceID)
		if err != nil {
			return errors.New("启用告警规则前，请先选择有效的数据源")
		}
		if !ds.Enabled {
			return errors.New("该数据源已停止，请先启用数据源后再启用告警规则")
		}
		if (isFileMonitorRule(rule) || isScriptMonitorRule(rule)) && !strings.EqualFold(ds.Type, "ssh") {
			return errors.New("SSH 检测规则仅支持 SSH 数据源")
		}
		if isDataMonitorRule(rule) && !strings.EqualFold(ds.Type, "mysql") {
			return errors.New("数据监控规则仅支持 MySQL 数据源")
		}
		if !isDataMonitorRule(rule) && !isFileMonitorRule(rule) && !isScriptMonitorRule(rule) && !strings.EqualFold(ds.Type, "prometheus") {
			return errors.New("指标阈值规则仅支持 Prometheus 数据源")
		}
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

// migrateStoredCredentials upgrades legacy plaintext rows in place at startup.
// Data-source credentials remain decryptable by the backend; user passwords do not.
func migrateStoredCredentials(ctx context.Context, current *sql.DB) error {
	rows, err := current.QueryContext(ctx, `SELECT id, COALESCE(password, '') FROM data_sources`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, password string
		if err := rows.Scan(&id, &password); err != nil {
			return err
		}
		if password == "" || strings.HasPrefix(password, encryptedCredentialPrefix) {
			continue
		}
		encrypted, err := encryptCredential(password)
		if err != nil {
			return err
		}
		if _, err := current.ExecContext(ctx, `UPDATE data_sources SET password = ? WHERE id = ?`, encrypted, id); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	users, err := current.QueryContext(ctx, `SELECT username, password FROM users`)
	if err != nil {
		return err
	}
	defer users.Close()
	for users.Next() {
		var username, password string
		if err := users.Scan(&username, &password); err != nil {
			return err
		}
		if strings.HasPrefix(password, "$2") {
			continue
		}
		hash, err := hashUserPassword(password)
		if err != nil {
			return err
		}
		if _, err := current.ExecContext(ctx, `UPDATE users SET password = ? WHERE username = ?`, hash, username); err != nil {
			return err
		}
	}
	return users.Err()
}
