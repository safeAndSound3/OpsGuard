package service

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

var prometheusMetricNameRe = regexp.MustCompile(`[^a-zA-Z0-9_:]`)

func TestMySQLDataSource(ds model.DataSource) error {
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SHOW GLOBAL STATUS LIKE 'Threads_connected'`)
	if err != nil {
		return err
	}
	return rows.Close()
}

func ExportMySQLPrometheusMetrics() string {
	sources := ListDataSources()
	var b strings.Builder
	writeMySQLMetricHeader(&b)
	for _, ds := range sources {
		if !ds.Enabled || !strings.EqualFold(ds.Type, "mysql") {
			continue
		}
		_ = FillDataSourcePassword(&ds)
		if err := appendMySQLDataSourceMetrics(&b, ds); err != nil {
			writePromMetric(&b, "opsguard_mysql_up", mysqlLabels(ds, nil), 0)
			writePromMetric(&b, "opsguard_mysql_scrape_error", mysqlLabels(ds, map[string]string{"error": err.Error()}), 1)
		}
	}
	return b.String()
}

func appendMySQLDataSourceMetrics(b *strings.Builder, ds model.DataSource) error {
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	writePromMetric(b, "opsguard_mysql_up", mysqlLabels(ds, nil), 1)

	status, err := mysqlKeyValueRows(ctx, db, `SHOW GLOBAL STATUS`)
	if err != nil {
		return err
	}
	variables, err := mysqlKeyValueRows(ctx, db, `SHOW GLOBAL VARIABLES`)
	if err != nil {
		return err
	}
	for _, key := range selectedMySQLStatusKeys(status) {
		if value, ok := parseFloat(status[key]); ok {
			writePromMetric(b, "opsguard_mysql_global_status", mysqlLabels(ds, map[string]string{"variable": key}), value)
		}
	}
	for _, key := range []string{"max_connections", "innodb_buffer_pool_size", "slow_query_log", "long_query_time"} {
		if value, ok := parseFloat(variables[key]); ok {
			writePromMetric(b, "opsguard_mysql_global_variable", mysqlLabels(ds, map[string]string{"variable": key}), value)
		}
	}
	if requests, ok1 := parseFloat(status["Innodb_buffer_pool_read_requests"]); ok1 && requests > 0 {
		if reads, ok2 := parseFloat(status["Innodb_buffer_pool_reads"]); ok2 {
			writePromMetric(b, "opsguard_mysql_innodb_buffer_pool_hit_ratio", mysqlLabels(ds, nil), (requests-reads)/requests)
		}
	}
	if seconds, ok := mysqlReplicationLagSeconds(ctx, db); ok {
		writePromMetric(b, "opsguard_mysql_replication_seconds_behind_master", mysqlLabels(ds, nil), seconds)
	}
	if innodb, err := mysqlInnodbStatusText(ctx, db); err == nil {
		for name, value := range parseInnodbStatusMetrics(innodb) {
			writePromMetric(b, "opsguard_mysql_innodb_status", mysqlLabels(ds, map[string]string{"variable": name}), value)
		}
	}
	return nil
}

func openMySQLDataSource(ds model.DataSource) (*sql.DB, error) {
	user := strings.TrimSpace(ds.Username)
	if user == "" {
		user = "root"
	}
	port := strings.TrimSpace(ds.Port)
	if port == "" {
		port = "3306"
	}
	database := primaryDatabase(ds.Database)
	path := "/"
	if database != "" {
		path += database
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)%s?parseTime=true&timeout=5s&readTimeout=8s&writeTimeout=8s&loc=Local", user, ds.Password, strings.TrimSpace(ds.Host), port, path)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Minute)
	return db, nil
}

func mysqlKeyValueRows(ctx context.Context, db *sql.DB, query string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		items[key] = value
	}
	return items, rows.Err()
}

func selectedMySQLStatusKeys(status map[string]string) []string {
	fixed := map[string]bool{
		"Threads_connected":                true,
		"Threads_running":                  true,
		"Slow_queries":                     true,
		"Questions":                        true,
		"Innodb_buffer_pool_reads":         true,
		"Innodb_buffer_pool_read_requests": true,
		"Innodb_row_lock_current_waits":    true,
		"Innodb_row_lock_time":             true,
		"Innodb_row_lock_time_avg":         true,
		"Innodb_row_lock_time_max":         true,
		"Innodb_row_lock_waits":            true,
	}
	keys := make([]string, 0, len(status))
	for key := range status {
		if fixed[key] || strings.HasPrefix(key, "Com_") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func mysqlReplicationLagSeconds(ctx context.Context, db *sql.DB) (float64, bool) {
	for _, query := range []string{`SHOW REPLICA STATUS`, `SHOW SLAVE STATUS`} {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			continue
		}
		cols, _ := rows.Columns()
		if rows.Next() {
			values := make([]sql.NullString, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err == nil {
				for i, col := range cols {
					if col == "Seconds_Behind_Master" || col == "Seconds_Behind_Source" {
						_ = rows.Close()
						if values[i].Valid {
							v, ok := parseFloat(values[i].String)
							return v, ok
						}
						return 0, false
					}
				}
			}
		}
		_ = rows.Close()
	}
	return 0, false
}

func mysqlInnodbStatusText(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `SHOW ENGINE INNODB STATUS`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", sql.ErrNoRows
	}
	var engine, name, status string
	if err := rows.Scan(&engine, &name, &status); err != nil {
		return "", err
	}
	return status, nil
}

func parseInnodbStatusMetrics(text string) map[string]float64 {
	metrics := map[string]float64{}
	patterns := map[string]*regexp.Regexp{
		"os_wait_array_reservation_count": regexp.MustCompile(`reservation count (\d+)`),
		"os_wait_array_signal_count":      regexp.MustCompile(`signal count (\d+)`),
		"history_list_length":             regexp.MustCompile(`History list length (\d+)`),
		"pending_reads":                   regexp.MustCompile(`Pending normal aio reads: (\d+)`),
		"pending_writes":                  regexp.MustCompile(`aio writes: (\d+)`),
	}
	for name, re := range patterns {
		if match := re.FindStringSubmatch(text); len(match) == 2 {
			if value, ok := parseFloat(match[1]); ok {
				metrics[name] = value
			}
		}
	}
	return metrics
}

func writeMySQLMetricHeader(b *strings.Builder) {
	b.WriteString("# HELP opsguard_mysql_up MySQL datasource scrape health, 1 means healthy.\n")
	b.WriteString("# TYPE opsguard_mysql_up gauge\n")
	b.WriteString("# HELP opsguard_mysql_scrape_error MySQL datasource scrape error flag.\n")
	b.WriteString("# TYPE opsguard_mysql_scrape_error gauge\n")
	b.WriteString("# HELP opsguard_mysql_global_status Numeric values from SHOW GLOBAL STATUS.\n")
	b.WriteString("# TYPE opsguard_mysql_global_status gauge\n")
	b.WriteString("# HELP opsguard_mysql_global_variable Numeric values from SHOW GLOBAL VARIABLES.\n")
	b.WriteString("# TYPE opsguard_mysql_global_variable gauge\n")
	b.WriteString("# HELP opsguard_mysql_innodb_buffer_pool_hit_ratio InnoDB buffer pool read hit ratio.\n")
	b.WriteString("# TYPE opsguard_mysql_innodb_buffer_pool_hit_ratio gauge\n")
	b.WriteString("# HELP opsguard_mysql_replication_seconds_behind_master MySQL replication lag seconds.\n")
	b.WriteString("# TYPE opsguard_mysql_replication_seconds_behind_master gauge\n")
	b.WriteString("# HELP opsguard_mysql_innodb_status Parsed numeric values from SHOW ENGINE INNODB STATUS.\n")
	b.WriteString("# TYPE opsguard_mysql_innodb_status gauge\n")
}

func mysqlLabels(ds model.DataSource, extra map[string]string) map[string]string {
	labels := map[string]string{
		"source_id":   ds.ID,
		"source_name": ds.Name,
		"host":        ds.Host,
		"port":        ds.Port,
	}
	for k, v := range extra {
		labels[k] = v
	}
	return labels
}

func writePromMetric(b *strings.Builder, name string, labels map[string]string, value float64) {
	name = prometheusMetricNameRe.ReplaceAllString(name, "_")
	b.WriteString(name)
	if len(labels) > 0 {
		keys := make([]string, 0, len(labels))
		for key := range labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(key)
			b.WriteString("=\"")
			b.WriteString(escapePromLabel(labels[key]))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	b.WriteByte('\n')
}

func escapePromLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func parseFloat(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "NULL") {
		return 0, false
	}
	if strings.EqualFold(value, "ON") {
		return 1, true
	}
	if strings.EqualFold(value, "OFF") {
		return 0, true
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil
}
