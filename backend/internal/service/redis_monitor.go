package service

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"monitor-platform/internal/model"
)

const (
	redisMonitorInterval = time.Minute
	redisMonitorTimeout  = 8 * time.Second
	redisSnapshotLimit   = 5000
)

var redisMonitorOnce sync.Once

func initRedisMonitorStore(appDB *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS redis_metric_snapshots (
			id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
			source_id varchar(64) NOT NULL,
			collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
			metrics_json json NOT NULL,
			INDEX idx_redis_metric_source_time (source_id, collected_at)
		)`,
		`CREATE TABLE IF NOT EXISTS redis_instance_status (
			source_id varchar(64) NOT NULL PRIMARY KEY,
			source_name varchar(120) NOT NULL,
			host varchar(255) NOT NULL,
			port varchar(16) NOT NULL,
			status varchar(32) NOT NULL,
			version varchar(120) NULL,
			uptime_seconds bigint NOT NULL DEFAULT 0,
			connected_clients bigint NOT NULL DEFAULT 0,
			blocked_clients bigint NOT NULL DEFAULT 0,
			used_memory bigint NOT NULL DEFAULT 0,
			max_memory bigint NOT NULL DEFAULT 0,
			memory_fragmentation decimal(10,3) NOT NULL DEFAULT 0,
			ops_per_second bigint NOT NULL DEFAULT 0,
			total_commands bigint NOT NULL DEFAULT 0,
			hit_rate decimal(10,4) NOT NULL DEFAULT 0,
			evicted_keys bigint NOT NULL DEFAULT 0,
			expired_keys bigint NOT NULL DEFAULT 0,
			rejected_connections bigint NOT NULL DEFAULT 0,
			slowlog_length bigint NOT NULL DEFAULT 0,
			key_count bigint NOT NULL DEFAULT 0,
			role varchar(64) NULL,
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

func startRedisMonitorCollector() {
	redisMonitorOnce.Do(func() {
		go func() {
			collectAllRedisInstances()
			ticker := time.NewTicker(redisMonitorInterval)
			defer ticker.Stop()
			for range ticker.C {
				collectAllRedisInstances()
			}
		}()
	})
}

func collectAllRedisInstances() {
	appDB := currentStore()
	if appDB == nil {
		return
	}
	sources, err := listRedisDataSourcesWithSecrets(appDB)
	if err != nil {
		log.Printf("redis monitor: list data sources failed: %v", err)
		return
	}
	for _, ds := range sources {
		if err := collectRedisInstance(appDB, ds); err != nil {
			log.Printf("redis monitor: collect %s failed: %v", ds.ID, err)
		}
	}
	cleanupRedisMonitorData(appDB)
}

func listRedisDataSourcesWithSecrets(appDB *sql.DB) ([]model.DataSource, error) {
	rows, err := appDB.Query(`SELECT id, name, type, host, port, COALESCE(username, ''), COALESCE(password, ''), COALESCE(database_name, '')
		FROM data_sources WHERE LOWER(type) = 'redis' AND enabled = 1 ORDER BY created_at DESC`)
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

func collectRedisInstance(appDB *sql.DB, ds model.DataSource) error {
	ctx, cancel := context.WithTimeout(context.Background(), redisMonitorTimeout)
	defer cancel()

	client, err := openRedisTarget(ctx, ds)
	if err != nil {
		saveRedisInstanceFailure(appDB, ds, err)
		return err
	}
	defer client.Close()

	infoRaw, err := client.DoBulkString("INFO")
	if err != nil {
		saveRedisInstanceFailure(appDB, ds, err)
		return err
	}
	metrics := parseRedisInfo(infoRaw)
	keyCount := redisSelectedKeyCount(metrics, ds.Database)
	if keyCount == 0 {
		keyCount = redisIntegerOrZero(client.Do("DBSIZE"))
	}
	metrics["dbsize"] = strconv.FormatInt(keyCount, 10)
	metrics["key_count"] = strconv.FormatInt(keyCount, 10)
	metrics["slowlog_len"] = strconv.FormatInt(redisIntegerOrZero(client.Do("SLOWLOG", "LEN")), 10)
	if err := saveRedisMetricSnapshot(appDB, ds.ID, metrics); err != nil {
		return err
	}
	if err := saveRedisInstanceStatus(appDB, ds, metrics, "健康", ""); err != nil {
		return err
	}
	return nil
}

func openRedisTarget(ctx context.Context, ds model.DataSource) (*redisClient, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ds.Host, ds.Port))
	if err != nil {
		return nil, err
	}
	client := &redisClient{conn: conn, reader: bufio.NewReader(conn)}
	if strings.TrimSpace(ds.Password) != "" {
		args := []string{"AUTH"}
		if strings.TrimSpace(ds.Username) != "" {
			args = append(args, ds.Username)
		}
		args = append(args, ds.Password)
		if _, err := client.Do(args...); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	if database := redisDatabaseIndex(ds.Database); database != "" {
		if _, err := client.Do("SELECT", database); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	if _, err := client.Do("PING"); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func redisDatabaseIndex(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "db") {
		value = strings.TrimPrefix(strings.ToLower(value), "db")
	}
	if _, err := strconv.Atoi(value); err != nil {
		return ""
	}
	return value
}

func parseRedisInfo(raw string) map[string]string {
	metrics := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if strings.HasPrefix(key, "db") {
			metrics[key] = value
			continue
		}
		switch key {
		case "redis_version", "uptime_in_seconds", "connected_clients", "blocked_clients", "used_memory", "used_memory_peak",
			"maxmemory", "mem_fragmentation_ratio", "instantaneous_ops_per_sec", "total_commands_processed",
			"keyspace_hits", "keyspace_misses", "evicted_keys", "expired_keys", "rejected_connections",
			"latest_fork_usec", "connected_slaves", "role", "used_cpu_sys", "used_cpu_user":
			metrics[key] = value
		}
	}
	hits := redisMetricInt(metrics, "keyspace_hits")
	misses := redisMetricInt(metrics, "keyspace_misses")
	if hits+misses > 0 {
		metrics["hit_rate"] = fmt.Sprintf("%.4f", float64(hits)/float64(hits+misses))
	} else {
		metrics["hit_rate"] = "0"
	}
	metrics["key_count"] = strconv.FormatInt(redisKeyCount(metrics), 10)
	return metrics
}

func redisKeyCount(metrics map[string]string) int64 {
	return redisSelectedKeyCount(metrics, "")
}

func redisSelectedKeyCount(metrics map[string]string, database string) int64 {
	selected := map[string]bool{}
	for _, item := range strings.Split(database, ",") {
		name := strings.TrimSpace(strings.ToLower(item))
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "db") {
			name = "db" + name
		}
		selected[name] = true
	}
	var total int64
	for key, value := range metrics {
		if !strings.HasPrefix(key, "db") {
			continue
		}
		if len(selected) > 0 && !selected[strings.ToLower(key)] {
			continue
		}
		for _, part := range strings.Split(value, ",") {
			pair := strings.SplitN(part, "=", 2)
			if len(pair) == 2 && pair[0] == "keys" {
				total += parseInt64(pair[1])
			}
		}
	}
	return total
}

func saveRedisMetricSnapshot(appDB *sql.DB, sourceID string, metrics map[string]string) error {
	payload, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = appDB.ExecContext(ctx, `INSERT INTO redis_metric_snapshots (source_id, metrics_json) VALUES (?, ?)`, sourceID, string(payload))
	return err
}

func saveRedisInstanceStatus(appDB *sql.DB, ds model.DataSource, metrics map[string]string, status string, lastErr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := appDB.ExecContext(ctx, `INSERT INTO redis_instance_status
		(source_id, source_name, host, port, status, version, uptime_seconds, connected_clients, blocked_clients,
			used_memory, max_memory, memory_fragmentation, ops_per_second, total_commands, hit_rate, evicted_keys,
			expired_keys, rejected_connections, slowlog_length, key_count, role, last_error, last_collected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE source_name = VALUES(source_name), host = VALUES(host), port = VALUES(port), status = VALUES(status),
			version = VALUES(version), uptime_seconds = VALUES(uptime_seconds), connected_clients = VALUES(connected_clients),
			blocked_clients = VALUES(blocked_clients), used_memory = VALUES(used_memory), max_memory = VALUES(max_memory),
			memory_fragmentation = VALUES(memory_fragmentation), ops_per_second = VALUES(ops_per_second),
			total_commands = VALUES(total_commands), hit_rate = VALUES(hit_rate), evicted_keys = VALUES(evicted_keys),
			expired_keys = VALUES(expired_keys), rejected_connections = VALUES(rejected_connections),
			slowlog_length = VALUES(slowlog_length), key_count = VALUES(key_count), role = VALUES(role),
			last_error = VALUES(last_error), last_collected_at = VALUES(last_collected_at)`,
		ds.ID, ds.Name, ds.Host, ds.Port, status, metrics["redis_version"], redisMetricInt(metrics, "uptime_in_seconds"),
		redisMetricInt(metrics, "connected_clients"), redisMetricInt(metrics, "blocked_clients"), redisMetricInt(metrics, "used_memory"),
		redisMetricInt(metrics, "maxmemory"), redisMetricFloat(metrics, "mem_fragmentation_ratio"),
		redisMetricInt(metrics, "instantaneous_ops_per_sec"), redisMetricInt(metrics, "total_commands_processed"),
		redisMetricFloat(metrics, "hit_rate"), redisMetricInt(metrics, "evicted_keys"), redisMetricInt(metrics, "expired_keys"),
		redisMetricInt(metrics, "rejected_connections"), redisMetricInt(metrics, "slowlog_len"), redisMetricInt(metrics, "key_count"),
		metrics["role"], lastErr)
	return err
}

func saveRedisInstanceFailure(appDB *sql.DB, ds model.DataSource, err error) {
	_ = saveRedisInstanceStatus(appDB, ds, map[string]string{}, "异常", err.Error())
}

func cleanupRedisMonitorData(appDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = appDB.ExecContext(ctx, `DELETE FROM redis_metric_snapshots WHERE id NOT IN (
		SELECT id FROM (SELECT id FROM redis_metric_snapshots ORDER BY collected_at DESC, id DESC LIMIT ?) keep_rows
	)`, redisSnapshotLimit)
}

func ListRedisInstanceStatuses() []model.RedisInstanceStatus {
	appDB := currentStore()
	if appDB == nil {
		return []model.RedisInstanceStatus{}
	}
	rows, err := appDB.Query(`SELECT source_id, source_name, host, port, status, COALESCE(version, ''), uptime_seconds,
		connected_clients, blocked_clients, used_memory, max_memory, memory_fragmentation, ops_per_second,
		total_commands, hit_rate, evicted_keys, expired_keys, rejected_connections, slowlog_length, key_count,
		COALESCE(role, ''), COALESCE(last_error, ''), last_collected_at FROM redis_instance_status ORDER BY last_collected_at DESC`)
	if err != nil {
		return []model.RedisInstanceStatus{}
	}
	defer rows.Close()

	statuses := []model.RedisInstanceStatus{}
	for rows.Next() {
		var item model.RedisInstanceStatus
		if err := rows.Scan(&item.SourceID, &item.SourceName, &item.Host, &item.Port, &item.Status, &item.Version,
			&item.UptimeSeconds, &item.ConnectedClients, &item.BlockedClients, &item.UsedMemory, &item.MaxMemory,
			&item.MemoryFragmentation, &item.OpsPerSecond, &item.TotalCommands, &item.HitRate, &item.EvictedKeys,
			&item.ExpiredKeys, &item.RejectedConnections, &item.SlowlogLength, &item.KeyCount, &item.Role,
			&item.LastError, &item.LastCollectedAt); err != nil {
			continue
		}
		statuses = append(statuses, item)
	}
	return statuses
}

func ListRedisMetricSnapshots(sourceID string, limit int, start *time.Time, end *time.Time) []model.RedisMetricSnapshot {
	appDB := currentStore()
	if appDB == nil {
		return []model.RedisMetricSnapshot{}
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
	rows, err := appDB.Query(`SELECT id, source_id, collected_at, metrics_json FROM redis_metric_snapshots
		WHERE `+strings.Join(conditions, " AND ")+` ORDER BY collected_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return []model.RedisMetricSnapshot{}
	}
	defer rows.Close()

	items := []model.RedisMetricSnapshot{}
	for rows.Next() {
		var item model.RedisMetricSnapshot
		var raw string
		if err := rows.Scan(&item.ID, &item.SourceID, &item.CollectedAt, &raw); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(raw), &item.Metrics)
		items = append(items, item)
	}
	return items
}

func redisMetricInt(metrics map[string]string, key string) int64 {
	return parseInt64(metrics[key])
}

func redisMetricFloat(metrics map[string]string, key string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(metrics[key]), 64)
	if err != nil {
		return 0
	}
	return value
}

func parseInt64(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func redisIntegerOrZero(value any, err error) int64 {
	if err != nil {
		return 0
	}
	switch typed := value.(type) {
	case int64:
		return typed
	case string:
		return parseInt64(typed)
	default:
		return 0
	}
}

type redisClient struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (c *redisClient) Close() error {
	return c.conn.Close()
}

func (c *redisClient) DoBulkString(args ...string) (string, error) {
	value, err := c.Do(args...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("redis command returned %T", value)
	}
	return text, nil
}

func (c *redisClient) Do(args ...string) (any, error) {
	if err := c.conn.SetDeadline(time.Now().Add(redisMonitorTimeout)); err != nil {
		return nil, err
	}
	if _, err := c.conn.Write([]byte(encodeRedisCommand(args...))); err != nil {
		return nil, err
	}
	return readRedisRESP(c.reader)
}

func encodeRedisCommand(args ...string) string {
	var builder strings.Builder
	builder.WriteString("*")
	builder.WriteString(strconv.Itoa(len(args)))
	builder.WriteString("\r\n")
	for _, arg := range args {
		builder.WriteString("$")
		builder.WriteString(strconv.Itoa(len(arg)))
		builder.WriteString("\r\n")
		builder.WriteString(arg)
		builder.WriteString("\r\n")
	}
	return builder.String()
}

func readRedisRESP(reader *bufio.Reader) (any, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	switch prefix {
	case '+':
		return readRedisLine(reader)
	case '-':
		line, _ := readRedisLine(reader)
		return nil, fmt.Errorf("redis error: %s", line)
	case ':':
		line, err := readRedisLine(reader)
		if err != nil {
			return nil, err
		}
		return strconv.ParseInt(line, 10, 64)
	case '$':
		line, err := readRedisLine(reader)
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if size < 0 {
			return "", nil
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, err
		}
		return string(buf[:size]), nil
	case '*':
		line, err := readRedisLine(reader)
		if err != nil {
			return nil, err
		}
		count, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		values := make([]any, 0, count)
		for i := 0; i < count; i++ {
			value, err := readRedisRESP(reader)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unexpected redis response prefix %q", prefix)
	}
}

func readRedisLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
