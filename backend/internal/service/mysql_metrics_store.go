package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"monitor-platform/internal/model"
)

var (
	metricsMu sync.RWMutex
	metricsDB *sql.DB
)

func initMySQLMetricStore(host, port, user, password, database string) error {

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	metricsDSN := user + ":" + password + "@tcp(" + host + ":" + port + ")/" + database + "?parseTime=true&loc=Local&timeout=5s&readTimeout=8s&writeTimeout=8s" + mysqlSessionTimeZone
	next, err := sql.Open("mysql", metricsDSN)
	if err != nil {
		return err
	}
	next.SetMaxOpenConns(6)
	next.SetMaxIdleConns(3)
	next.SetConnMaxLifetime(5 * time.Minute)
	if err := next.PingContext(ctx); err != nil {
		return err
	}
	schema := `CREATE TABLE IF NOT EXISTS mysql_metric_samples (
		id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
		source_id varchar(64) NOT NULL,
		source_name varchar(120) NOT NULL,
		host varchar(255) NOT NULL,
		port varchar(16) NOT NULL,
		metric_group varchar(64) NOT NULL,
		metric_name varchar(160) NOT NULL,
		metric_value double NOT NULL,
		labels_json json NULL,
		collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_mysql_metric_samples_source_time (source_id, collected_at),
		INDEX idx_mysql_metric_samples_name_time (metric_group, metric_name, collected_at)
	)`
	if _, err := next.ExecContext(ctx, schema); err != nil {
		return err
	}

	metricsMu.Lock()
	if metricsDB != nil {
		_ = metricsDB.Close()
	}
	metricsDB = next
	metricsMu.Unlock()
	return nil
}

func startMySQLMetricCollector() {
	go func() {
		collectAndStoreMySQLMetrics()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			collectAndStoreMySQLMetrics()
		}
	}()
}

func collectAndStoreMySQLMetrics() {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return
	}
	for _, ds := range ListDataSources() {
		if !ds.Enabled || !strings.EqualFold(ds.Type, "mysql") {
			continue
		}
		_ = FillDataSourcePassword(&ds)
		metrics, err := collectMySQLDataSourceMetrics(ds)
		if err != nil {
			metrics = []mysqlPromMetric{{
				Name:   "opsguard_mysql_up",
				Labels: mysqlLabels(ds, nil),
				Value:  0,
			}, {
				Name:   "opsguard_mysql_scrape_error",
				Labels: mysqlLabels(ds, map[string]string{"error": err.Error()}),
				Value:  1,
			}}
		}
		_ = storeMySQLMetricSamples(current, ds, metrics)
	}
}

func storeMySQLMetricSamples(current *sql.DB, ds model.DataSource, metrics []mysqlPromMetric) error {
	if current == nil {
		return errors.New("metrics store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := current.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO mysql_metric_samples
		(source_id, source_name, host, port, metric_group, metric_name, metric_value, labels_json, collected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	collectedAt := time.Now()
	for _, metric := range metrics {
		group, name := splitMySQLMetricName(metric)
		labelsJSON, _ := json.Marshal(metric.Labels)
		if _, err := stmt.ExecContext(ctx, ds.ID, ds.Name, ds.Host, ds.Port, group, name, metric.Value, string(labelsJSON), collectedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func splitMySQLMetricName(metric mysqlPromMetric) (string, string) {
	if variable := strings.TrimSpace(metric.Labels["variable"]); variable != "" {
		return strings.TrimPrefix(metric.Name, "opsguard_mysql_"), variable
	}
	return strings.TrimPrefix(metric.Name, "opsguard_mysql_"), metric.Name
}

// LatestMySQLDashboardMetrics returns the most recently collected values for one instance.
// The dashboard deliberately reads the local collector store instead of relying on a
// separately configured Prometheus scrape target.
func LatestMySQLDashboardMetrics(sourceID string) (map[string]float64, time.Time, error) {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return nil, time.Time{}, errors.New("metrics store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := current.QueryContext(ctx, `SELECT metric_group, metric_name, metric_value, collected_at
		FROM mysql_metric_samples WHERE source_id = ? ORDER BY collected_at DESC, id DESC LIMIT 200`, sourceID)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	result := map[string]float64{}
	var collectedAt time.Time
	for rows.Next() {
		var group, name string
		var value float64
		var at time.Time
		if err := rows.Scan(&group, &name, &value, &at); err != nil {
			return nil, time.Time{}, err
		}
		if collectedAt.IsZero() {
			collectedAt = at
		}
		key := ""
		switch {
		case name == "opsguard_mysql_up":
			key = "up"
		case group == "global_status" && name == "Threads_connected":
			key = "threads"
		case group == "global_status" && name == "Threads_running":
			key = "running"
		case group == "global_status" && name == "Slow_queries":
			key = "slow"
		case group == "global_status" && name == "Questions":
			key = "questions"
		case name == "opsguard_mysql_innodb_buffer_pool_hit_ratio":
			key = "hit"
		}
		if key != "" {
			if _, exists := result[key]; !exists {
				result[key] = value
			}
		}
	}
	return result, collectedAt, rows.Err()
}
