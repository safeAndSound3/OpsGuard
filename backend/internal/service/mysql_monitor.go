package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"monitor-platform/internal/model"
)

const (
	mysqlMonitorInterval = time.Minute
	mysqlMonitorTimeout  = 15 * time.Second
	mysqlSnapshotLimit   = 5000
	mysqlSlowQueryLimit  = 2000
)

var mysqlMonitorOnce sync.Once

func initMySQLMonitorStore(appDB *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS mysql_metric_snapshots (
			id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
			source_id varchar(64) NOT NULL,
			collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
			metrics_json json NOT NULL,
			INDEX idx_mysql_metric_source_time (source_id, collected_at)
		)`,
		`CREATE TABLE IF NOT EXISTS mysql_slow_query_samples (
			id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
			source_id varchar(64) NOT NULL,
			schema_name varchar(128) NULL,
			digest varchar(128) NULL,
			query_text text NOT NULL,
			count_star bigint NOT NULL DEFAULT 0,
			total_latency_ms decimal(18,3) NOT NULL DEFAULT 0,
			avg_latency_ms decimal(18,3) NOT NULL DEFAULT 0,
			max_latency_ms decimal(18,3) NOT NULL DEFAULT 0,
			rows_examined bigint NOT NULL DEFAULT 0,
			rows_sent bigint NOT NULL DEFAULT 0,
			first_seen timestamp NULL,
			last_seen timestamp NULL,
			collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_mysql_slow_source_time (source_id, collected_at),
			INDEX idx_mysql_slow_digest (source_id, digest)
		)`,
		`CREATE TABLE IF NOT EXISTS mysql_instance_status (
			source_id varchar(64) NOT NULL PRIMARY KEY,
			source_name varchar(120) NOT NULL,
			host varchar(255) NOT NULL,
			port varchar(16) NOT NULL,
			status varchar(32) NOT NULL,
			version varchar(120) NULL,
			uptime_seconds bigint NOT NULL DEFAULT 0,
			threads_connected bigint NOT NULL DEFAULT 0,
			max_connections bigint NOT NULL DEFAULT 0,
			slow_queries bigint NOT NULL DEFAULT 0,
			questions bigint NOT NULL DEFAULT 0,
			database_size_bytes bigint NOT NULL DEFAULT 0,
			replica_status varchar(64) NULL,
			last_error text NULL,
			last_collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		)`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, statement := range statements {
		if _, err := appDB.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func startMySQLMonitorCollector() {
	mysqlMonitorOnce.Do(func() {
		go func() {
			collectAllMySQLInstances()
			ticker := time.NewTicker(mysqlMonitorInterval)
			defer ticker.Stop()
			for range ticker.C {
				collectAllMySQLInstances()
			}
		}()
	})
}

func collectAllMySQLInstances() {
	appDB := currentStore()
	if appDB == nil {
		return
	}

	sources, err := listMySQLDataSourcesWithSecrets(appDB)
	if err != nil {
		log.Printf("mysql monitor: list data sources failed: %v", err)
		return
	}
	for _, ds := range sources {
		if err := collectMySQLInstance(appDB, ds); err != nil {
			log.Printf("mysql monitor: collect %s failed: %v", ds.ID, err)
		}
	}
	cleanupMySQLMonitorData(appDB)
}

func listMySQLDataSourcesWithSecrets(appDB *sql.DB) ([]model.DataSource, error) {
	rows, err := appDB.Query(`SELECT id, name, type, host, port, COALESCE(username, ''), COALESCE(password, ''), COALESCE(database_name, '')
		FROM data_sources WHERE LOWER(type) = 'mysql' ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []model.DataSource
	for rows.Next() {
		var ds model.DataSource
		if err := rows.Scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Password, &ds.Database); err != nil {
			return nil, err
		}
		sources = append(sources, ds)
	}
	return sources, rows.Err()
}

func collectMySQLInstance(appDB *sql.DB, ds model.DataSource) error {
	ctx, cancel := context.WithTimeout(context.Background(), mysqlMonitorTimeout)
	defer cancel()

	targetDB, err := openMySQLTarget(ds)
	if err != nil {
		saveMySQLInstanceFailure(appDB, ds, err)
		return err
	}
	defer targetDB.Close()

	if err := targetDB.PingContext(ctx); err != nil {
		saveMySQLInstanceFailure(appDB, ds, err)
		return err
	}

	statusVars, statusErr := readNameValueRows(ctx, targetDB, `SHOW GLOBAL STATUS`)
	globalVars, varsErr := readNameValueRows(ctx, targetDB, `SHOW GLOBAL VARIABLES`)
	if statusErr != nil {
		saveMySQLInstanceFailure(appDB, ds, statusErr)
		return statusErr
	}
	if varsErr != nil {
		saveMySQLInstanceFailure(appDB, ds, varsErr)
		return varsErr
	}

	extra := map[string]string{}
	extra["database_size_bytes"] = strconv.FormatInt(readDatabaseSize(ctx, targetDB), 10)
	extra["process_running"] = strconv.FormatInt(readProcessCount(ctx, targetDB, "Query"), 10)
	extra["process_locked"] = strconv.FormatInt(readProcessCount(ctx, targetDB, "Locked"), 10)
	extra["replica_status"] = readReplicaStatus(ctx, targetDB)

	metrics := buildMySQLMetrics(statusVars, globalVars, extra)
	if err := saveMySQLMetricSnapshot(appDB, ds.ID, metrics); err != nil {
		return err
	}
	if err := saveMySQLInstanceStatus(appDB, ds, metrics, "健康", ""); err != nil {
		return err
	}
	if err := collectMySQLSlowQueries(ctx, appDB, targetDB, ds.ID); err != nil {
		log.Printf("mysql monitor: slow query digest unavailable for %s: %v", ds.ID, err)
	}
	return nil
}

func openMySQLTarget(ds model.DataSource) (*sql.DB, error) {
	targetDB := primaryDatabase(ds.Database)
	if targetDB == "" {
		targetDB = "mysql"
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?timeout=5s&readTimeout=8s&writeTimeout=8s&parseTime=true&loc=Local",
		ds.Username, ds.Password, ds.Host, ds.Port, targetDB)
	return sql.Open("mysql", dsn)
}

func readNameValueRows(ctx context.Context, targetDB *sql.DB, query string) (map[string]string, error) {
	rows, err := targetDB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	values := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		values[name] = value
	}
	return values, rows.Err()
}

func buildMySQLMetrics(statusVars map[string]string, globalVars map[string]string, extra map[string]string) map[string]string {
	metrics := map[string]string{}
	copyMetricKeys(metrics, statusVars, []string{
		"Aborted_clients", "Aborted_connects", "Bytes_received", "Bytes_sent",
		"Com_delete", "Com_insert", "Com_select", "Com_update", "Connections",
		"Created_tmp_disk_tables", "Created_tmp_tables", "Handler_read_rnd_next",
		"Innodb_buffer_pool_pages_dirty", "Innodb_buffer_pool_pages_free", "Innodb_buffer_pool_pages_total",
		"Innodb_buffer_pool_read_requests", "Innodb_buffer_pool_reads", "Innodb_data_fsyncs",
		"Innodb_log_waits", "Innodb_row_lock_current_waits", "Innodb_row_lock_time",
		"Innodb_row_lock_waits", "Max_used_connections",
		"Questions", "Queries", "Select_full_join", "Select_scan", "Slow_queries",
		"Table_locks_waited", "Threads_connected", "Threads_running", "Uptime",
	})
	copyMetricKeys(metrics, globalVars, []string{
		"innodb_buffer_pool_size", "max_connections", "version",
	})
	for key, value := range extra {
		metrics[key] = value
	}
	return metrics
}

func copyMetricKeys(target map[string]string, source map[string]string, keys []string) {
	for _, key := range keys {
		if value, ok := source[key]; ok {
			target[key] = value
		}
	}
}

func readDatabaseSize(ctx context.Context, targetDB *sql.DB) int64 {
	var size sql.NullInt64
	err := targetDB.QueryRowContext(ctx, `SELECT COALESCE(SUM(data_length + index_length), 0) FROM information_schema.tables`).Scan(&size)
	if err != nil || !size.Valid {
		return 0
	}
	return size.Int64
}

func readProcessCount(ctx context.Context, targetDB *sql.DB, state string) int64 {
	var count int64
	err := targetDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.processlist WHERE COALESCE(state, command, '') LIKE ?`, "%"+state+"%").Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

func readReplicaStatus(ctx context.Context, targetDB *sql.DB) string {
	if status := scanReplicaStatus(ctx, targetDB, "SHOW REPLICA STATUS"); status != "" {
		return status
	}
	if status := scanReplicaStatus(ctx, targetDB, "SHOW SLAVE STATUS"); status != "" {
		return status
	}
	return "not_replica_or_no_privilege"
}

func scanReplicaStatus(ctx context.Context, targetDB *sql.DB, query string) string {
	rows, err := targetDB.QueryContext(ctx, query)
	if err != nil {
		return ""
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !rows.Next() {
		return ""
	}
	values := make([]sql.NullString, len(cols))
	args := make([]any, len(cols))
	for i := range values {
		args[i] = &values[i]
	}
	if err := rows.Scan(args...); err != nil {
		return ""
	}
	result := map[string]string{}
	for i, col := range cols {
		if values[i].Valid {
			result[col] = values[i].String
		}
	}
	if result["Replica_IO_Running"] == "Yes" && result["Replica_SQL_Running"] == "Yes" {
		return "running"
	}
	if result["Slave_IO_Running"] == "Yes" && result["Slave_SQL_Running"] == "Yes" {
		return "running"
	}
	if len(result) > 0 {
		return "error"
	}
	return ""
}

func saveMySQLMetricSnapshot(appDB *sql.DB, sourceID string, metrics map[string]string) error {
	payload, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = appDB.ExecContext(ctx, `INSERT INTO mysql_metric_snapshots (source_id, metrics_json) VALUES (?, ?)`, sourceID, string(payload))
	return err
}

func saveMySQLInstanceStatus(appDB *sql.DB, ds model.DataSource, metrics map[string]string, status string, lastErr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := appDB.ExecContext(ctx, `INSERT INTO mysql_instance_status
		(source_id, source_name, host, port, status, version, uptime_seconds, threads_connected, max_connections, slow_queries, questions, database_size_bytes, replica_status, last_error, last_collected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE source_name = VALUES(source_name), host = VALUES(host), port = VALUES(port), status = VALUES(status),
			version = VALUES(version), uptime_seconds = VALUES(uptime_seconds), threads_connected = VALUES(threads_connected),
			max_connections = VALUES(max_connections), slow_queries = VALUES(slow_queries), questions = VALUES(questions),
			database_size_bytes = VALUES(database_size_bytes), replica_status = VALUES(replica_status), last_error = VALUES(last_error),
			last_collected_at = VALUES(last_collected_at)`,
		ds.ID, ds.Name, ds.Host, ds.Port, status, metrics["version"], metricInt(metrics, "Uptime"),
		metricInt(metrics, "Threads_connected"), metricInt(metrics, "max_connections"), metricInt(metrics, "Slow_queries"),
		metricInt(metrics, "Questions"), metricInt(metrics, "database_size_bytes"), metrics["replica_status"], lastErr)
	return err
}

func saveMySQLInstanceFailure(appDB *sql.DB, ds model.DataSource, err error) {
	metrics := map[string]string{}
	_ = saveMySQLInstanceStatus(appDB, ds, metrics, "异常", err.Error())
}

func collectMySQLSlowQueries(ctx context.Context, appDB *sql.DB, targetDB *sql.DB, sourceID string) error {
	rows, err := targetDB.QueryContext(ctx, `SELECT
			COALESCE(SCHEMA_NAME, ''),
			COALESCE(DIGEST, ''),
			LEFT(COALESCE(DIGEST_TEXT, ''), 4096),
			COUNT_STAR,
			SUM_TIMER_WAIT / 1000000000,
			AVG_TIMER_WAIT / 1000000000,
			MAX_TIMER_WAIT / 1000000000,
			SUM_ROWS_EXAMINED,
			SUM_ROWS_SENT,
			COALESCE(FIRST_SEEN, NOW()),
			COALESCE(LAST_SEEN, NOW())
		FROM performance_schema.events_statements_summary_by_digest
		WHERE DIGEST_TEXT IS NOT NULL
		ORDER BY SUM_TIMER_WAIT DESC
		LIMIT 20`)
	if err != nil {
		return err
	}
	defer rows.Close()

	insertCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for rows.Next() {
		var schemaName, digest, queryText string
		var count, rowsExamined, rowsSent int64
		var totalLatencyMs, avgLatencyMs, maxLatencyMs float64
		var firstSeen, lastSeen time.Time
		if err := rows.Scan(&schemaName, &digest, &queryText, &count, &totalLatencyMs, &avgLatencyMs, &maxLatencyMs, &rowsExamined, &rowsSent, &firstSeen, &lastSeen); err != nil {
			return err
		}
		if strings.TrimSpace(queryText) == "" {
			continue
		}
		_, err := appDB.ExecContext(insertCtx, `INSERT INTO mysql_slow_query_samples
			(source_id, schema_name, digest, query_text, count_star, total_latency_ms, avg_latency_ms, max_latency_ms, rows_examined, rows_sent, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sourceID, schemaName, digest, queryText, count, totalLatencyMs, avgLatencyMs, maxLatencyMs, rowsExamined, rowsSent, firstSeen, lastSeen)
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func cleanupMySQLMonitorData(appDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = appDB.ExecContext(ctx, `DELETE FROM mysql_metric_snapshots WHERE id NOT IN (
		SELECT id FROM (SELECT id FROM mysql_metric_snapshots ORDER BY collected_at DESC, id DESC LIMIT ?) keep_rows
	)`, mysqlSnapshotLimit)
	_, _ = appDB.ExecContext(ctx, `DELETE FROM mysql_slow_query_samples WHERE id NOT IN (
		SELECT id FROM (SELECT id FROM mysql_slow_query_samples ORDER BY collected_at DESC, id DESC LIMIT ?) keep_rows
	)`, mysqlSlowQueryLimit)
}

func ListMySQLInstanceStatuses() []model.MySQLInstanceStatus {
	appDB := currentStore()
	if appDB == nil {
		return []model.MySQLInstanceStatus{}
	}
	rows, err := appDB.Query(`SELECT source_id, source_name, host, port, status, COALESCE(version, ''), uptime_seconds,
		threads_connected, max_connections, slow_queries, questions, database_size_bytes, COALESCE(replica_status, ''),
		COALESCE(last_error, ''), last_collected_at FROM mysql_instance_status ORDER BY last_collected_at DESC`)
	if err != nil {
		return []model.MySQLInstanceStatus{}
	}
	defer rows.Close()

	statuses := []model.MySQLInstanceStatus{}
	for rows.Next() {
		var item model.MySQLInstanceStatus
		if err := rows.Scan(&item.SourceID, &item.SourceName, &item.Host, &item.Port, &item.Status, &item.Version,
			&item.UptimeSeconds, &item.ThreadsConnected, &item.MaxConnections, &item.SlowQueries, &item.Questions,
			&item.DatabaseSizeBytes, &item.ReplicaStatus, &item.LastError, &item.LastCollectedAt); err != nil {
			continue
		}
		statuses = append(statuses, item)
	}
	return statuses
}

func ListMySQLMetricSnapshots(sourceID string, limit int, start *time.Time, end *time.Time) []model.MySQLMetricSnapshot {
	appDB := currentStore()
	if appDB == nil {
		return []model.MySQLMetricSnapshot{}
	}
	limit = normalizeLimit(limit, 100)
	conditions := []string{"source_id = ?"}
	args := []any{sourceID}
	if start != nil {
		conditions = append(conditions, "collected_at >= ?")
		args = append(args, *start)
	}
	if end != nil {
		conditions = append(conditions, "collected_at <= ?")
		args = append(args, *end)
	}
	args = append(args, limit)
	rows, err := appDB.Query(`SELECT id, source_id, collected_at, metrics_json FROM mysql_metric_snapshots
		WHERE `+strings.Join(conditions, " AND ")+` ORDER BY collected_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return []model.MySQLMetricSnapshot{}
	}
	defer rows.Close()

	items := []model.MySQLMetricSnapshot{}
	for rows.Next() {
		var item model.MySQLMetricSnapshot
		var raw string
		if err := rows.Scan(&item.ID, &item.SourceID, &item.CollectedAt, &raw); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(raw), &item.Metrics)
		items = append(items, item)
	}
	return items
}

func ListMySQLSlowQueries(sourceID string, limit int, start *time.Time, end *time.Time) []model.MySQLSlowQuerySample {
	appDB := currentStore()
	if appDB == nil {
		return []model.MySQLSlowQuerySample{}
	}
	limit = normalizeLimit(limit, 100)
	conditions := []string{"source_id = ?"}
	args := []any{sourceID}
	if start != nil {
		conditions = append(conditions, "collected_at >= ?")
		args = append(args, *start)
	}
	if end != nil {
		conditions = append(conditions, "collected_at <= ?")
		args = append(args, *end)
	}
	args = append(args, limit)
	rows, err := appDB.Query(`SELECT id, source_id, COALESCE(schema_name, ''), COALESCE(digest, ''), query_text,
		count_star, total_latency_ms, avg_latency_ms, max_latency_ms, rows_examined, rows_sent,
		COALESCE(first_seen, collected_at), COALESCE(last_seen, collected_at), collected_at
		FROM mysql_slow_query_samples WHERE `+strings.Join(conditions, " AND ")+` ORDER BY collected_at DESC, total_latency_ms DESC LIMIT ?`, args...)
	if err != nil {
		return []model.MySQLSlowQuerySample{}
	}
	defer rows.Close()

	items := []model.MySQLSlowQuerySample{}
	for rows.Next() {
		var item model.MySQLSlowQuerySample
		if err := rows.Scan(&item.ID, &item.SourceID, &item.SchemaName, &item.Digest, &item.QueryText, &item.Count,
			&item.TotalLatencyMs, &item.AverageLatencyMs, &item.MaxLatencyMs, &item.RowsExamined, &item.RowsSent,
			&item.FirstSeen, &item.LastSeen, &item.CollectedAt); err != nil {
			continue
		}
		items = append(items, item)
	}
	return items
}

func currentStore() *sql.DB {
	mu.RLock()
	defer mu.RUnlock()
	return db
}

func metricInt(metrics map[string]string, key string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(metrics[key]), 10, 64)
	return value
}

func normalizeLimit(limit int, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > 500 {
		return 500
	}
	return limit
}
