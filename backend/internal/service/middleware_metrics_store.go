package service

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

func initMiddlewareMetricStore() error {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return errors.New("metrics store is not initialized")
	}
	_, err := current.Exec(`CREATE TABLE IF NOT EXISTS middleware_metric_samples (
		id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
		source_id varchar(64) NOT NULL,
		source_type varchar(32) NOT NULL,
		metric_name varchar(120) NOT NULL,
		metric_value double NOT NULL,
		collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_middleware_metric_source_time (source_id, collected_at),
		INDEX idx_middleware_metric_type_time (source_type, collected_at)
	)`)
	return err
}

func startMiddlewareMetricCollector() {
	go func() {
		collectAndStoreMiddlewareMetrics()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			collectAndStoreMiddlewareMetrics()
		}
	}()
}

func collectAndStoreMiddlewareMetrics() {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return
	}
	for _, ds := range ListDataSources() {
		if !ds.Enabled || !isMiddlewareMetricSource(ds.Type) {
			continue
		}
		_ = FillDataSourcePassword(&ds)
		metrics, err := collectMiddlewareMetrics(ds)
		if err != nil {
			metrics = map[string]float64{"up": 0}
		} else {
			metrics["up"] = 1
		}
		_ = storeMiddlewareMetrics(current, ds, metrics)
	}
}

func isMiddlewareMetricSource(sourceType string) bool {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "redis", "clickhouse", "kafka":
		return true
	default:
		return false
	}
}

func storeMiddlewareMetrics(current *sql.DB, ds model.DataSource, metrics map[string]float64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	stmt, err := current.PrepareContext(ctx, `INSERT INTO middleware_metric_samples (source_id, source_type, metric_name, metric_value, collected_at) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	at := time.Now()
	for _, name := range names {
		if _, err := stmt.ExecContext(ctx, ds.ID, ds.Type, name, metrics[name], at); err != nil {
			return err
		}
	}
	return nil
}

func LatestMiddlewareDashboardMetrics(sourceID string) (map[string]float64, time.Time, error) {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return nil, time.Time{}, errors.New("metrics store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := current.QueryContext(ctx, `SELECT metric_name, metric_value, collected_at FROM middleware_metric_samples WHERE source_id = ? ORDER BY collected_at DESC, id DESC LIMIT 100`, sourceID)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	metrics := map[string]float64{}
	var collectedAt time.Time
	for rows.Next() {
		var name string
		var value float64
		var at time.Time
		if err := rows.Scan(&name, &value, &at); err != nil {
			return nil, time.Time{}, err
		}
		if collectedAt.IsZero() {
			collectedAt = at
		}
		if _, exists := metrics[name]; !exists {
			metrics[name] = value
		}
	}
	return metrics, collectedAt, rows.Err()
}

func collectMiddlewareMetrics(ds model.DataSource) (map[string]float64, error) {
	switch strings.ToLower(strings.TrimSpace(ds.Type)) {
	case "redis":
		return collectRedisMetrics(ds)
	case "clickhouse":
		return collectClickHouseMetrics(ds)
	case "kafka":
		return collectKafkaMetrics(ds)
	default:
		return nil, errors.New("unsupported middleware source")
	}
}

func collectRedisMetrics(ds model.DataSource) (map[string]float64, error) {
	info, err := redisInfo(ds)
	if err != nil {
		return nil, err
	}
	get := func(name string) float64 { value, _ := strconv.ParseFloat(info[name], 64); return value }
	metrics := map[string]float64{
		"connected_clients":     get("connected_clients"),
		"blocked_clients":       get("blocked_clients"),
		"used_memory_bytes":     get("used_memory"),
		"max_memory_bytes":      get("maxmemory"),
		"operations_per_second": get("instantaneous_ops_per_sec"),
		"keyspace_hits":         get("keyspace_hits"),
		"keyspace_misses":       get("keyspace_misses"),
		"uptime_seconds":        get("uptime_in_seconds"),
	}
	if total := metrics["keyspace_hits"] + metrics["keyspace_misses"]; total > 0 {
		metrics["hit_ratio"] = metrics["keyspace_hits"] / total
	}
	if maxMemory := metrics["max_memory_bytes"]; maxMemory > 0 {
		metrics["memory_usage_ratio"] = metrics["used_memory_bytes"] / maxMemory
	}
	for key, value := range info {
		if !strings.HasPrefix(key, "db") {
			continue
		}
		for _, part := range strings.Split(value, ",") {
			if strings.HasPrefix(part, "keys=") {
				metrics["keys"] += parseMetricNumber(strings.TrimPrefix(part, "keys="))
			}
		}
	}
	return metrics, nil
}

func redisInfo(ds model.DataSource) (map[string]string, error) {
	connection, err := net.DialTimeout("tcp", middlewareAddress(ds), 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(6 * time.Second))
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	if password := strings.TrimSpace(ds.Password); password != "" {
		arguments := []string{"AUTH", password}
		if username := strings.TrimSpace(ds.Username); username != "" {
			arguments = []string{"AUTH", username, password}
		}
		if _, err := redisExecute(reader, writer, arguments...); err != nil {
			return nil, err
		}
	}
	body, err := redisExecute(reader, writer, "INFO")
	if err != nil {
		return nil, err
	}
	info := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			info[key] = value
		}
	}
	return info, nil
}

func redisExecute(reader *bufio.Reader, writer *bufio.Writer, values ...string) (string, error) {
	if _, err := fmt.Fprintf(writer, "*%d\r\n", len(values)); err != nil {
		return "", err
	}
	for _, value := range values {
		if _, err := fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(value), value); err != nil {
			return "", err
		}
	}
	if err := writer.Flush(); err != nil {
		return "", err
	}
	prefix, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if prefix == '-' {
		return "", errors.New(line)
	}
	if prefix == '+' {
		return line, nil
	}
	if prefix != '$' {
		return "", fmt.Errorf("unsupported Redis response %q", prefix)
	}
	length, err := strconv.Atoi(line)
	if err != nil || length < 0 {
		return "", fmt.Errorf("invalid Redis bulk response")
	}
	payload := make([]byte, length+2)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return "", err
	}
	return string(payload[:length]), nil
}

func collectClickHouseMetrics(ds model.DataSource) (map[string]float64, error) {
	metrics, err := clickHouseMetricQuery(ds, "SELECT metric, value FROM system.metrics WHERE metric IN ('Query','TCPConnection','HTTPConnection','MemoryTracking','BackgroundPoolTask','TotalPartsOfMergeTreeTables') FORMAT JSONEachRow")
	if err != nil {
		return nil, err
	}
	async, _ := clickHouseMetricQuery(ds, "SELECT metric, value FROM system.asynchronous_metrics WHERE metric IN ('Uptime','OSMemoryAvailable','OSMemoryTotal') FORMAT JSONEachRow")
	result := map[string]float64{
		"active_queries":   metrics["Query"],
		"connections":      metrics["TCPConnection"] + metrics["HTTPConnection"],
		"memory_bytes":     metrics["MemoryTracking"],
		"background_tasks": metrics["BackgroundPoolTask"],
		"parts":            metrics["TotalPartsOfMergeTreeTables"],
		"uptime_seconds":   async["Uptime"],
	}
	if total := async["OSMemoryTotal"]; total > 0 {
		result["memory_usage_ratio"] = 1 - async["OSMemoryAvailable"]/total
	}
	return result, nil
}

func clickHouseMetricQuery(ds model.DataSource, query string) (map[string]float64, error) {
	endpoint := "http://" + middlewareAddress(ds) + "/?query=" + url.QueryEscape(query)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ds.Username) != "" {
		req.SetBasicAuth(ds.Username, ds.Password)
	}
	response, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("ClickHouse returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	result := map[string]float64{}
	for {
		var item struct {
			Metric string          `json:"metric"`
			Value  json.RawMessage `json:"value"`
		}
		if err := decoder.Decode(&item); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		value, err := parseClickHouseMetricValue(item.Value)
		if err != nil {
			return nil, err
		}
		result[item.Metric] = value
	}
	return result, nil
}

func parseClickHouseMetricValue(raw json.RawMessage) (float64, error) {
	value := strings.TrimSpace(string(raw))
	if unquoted, err := strconv.Unquote(value); err == nil {
		value = unquoted
	}
	return strconv.ParseFloat(value, 64)
}

func collectKafkaMetrics(ds model.DataSource) (map[string]float64, error) {
	connection, err := net.DialTimeout("tcp", middlewareAddress(ds), 6*time.Second)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(7 * time.Second))
	request := bytes.Buffer{}
	_ = binary.Write(&request, binary.BigEndian, int16(3)) // Metadata API
	_ = binary.Write(&request, binary.BigEndian, int16(0)) // Kafka protocol v0 is broadly compatible
	_ = binary.Write(&request, binary.BigEndian, int32(1))
	kafkaWriteString(&request, "opsguard")
	_ = binary.Write(&request, binary.BigEndian, int32(0)) // all topics for Metadata API v0
	payload := request.Bytes()
	if err := binary.Write(connection, binary.BigEndian, int32(len(payload))); err != nil {
		return nil, err
	}
	if _, err := connection.Write(payload); err != nil {
		return nil, err
	}
	var responseLength int32
	if err := binary.Read(connection, binary.BigEndian, &responseLength); err != nil {
		return nil, fmt.Errorf("read Kafka metadata length: %w", err)
	}
	if responseLength <= 0 || responseLength > 8<<20 {
		return nil, errors.New("invalid Kafka metadata response")
	}
	response := make([]byte, responseLength)
	if _, err := io.ReadFull(connection, response); err != nil {
		return nil, err
	}
	reader := bytes.NewReader(response)
	var correlationID int32
	if err := binary.Read(reader, binary.BigEndian, &correlationID); err != nil || correlationID != 1 {
		return nil, errors.New("invalid Kafka metadata correlation ID")
	}
	brokers, err := kafkaReadArrayLength(reader)
	if err != nil {
		return nil, err
	}
	for i := 0; i < brokers; i++ {
		var nodeID, port int32
		if err := binary.Read(reader, binary.BigEndian, &nodeID); err != nil {
			return nil, err
		}
		if _, err := kafkaReadString(reader); err != nil {
			return nil, err
		}
		if err := binary.Read(reader, binary.BigEndian, &port); err != nil {
			return nil, err
		}
	}
	topics, err := kafkaReadArrayLength(reader)
	if err != nil {
		return nil, err
	}
	partitions := 0
	availableTopics := 0
	for i := 0; i < topics; i++ {
		var topicError int16
		if err := binary.Read(reader, binary.BigEndian, &topicError); err != nil {
			return nil, err
		}
		if _, err := kafkaReadString(reader); err != nil {
			return nil, err
		}
		count, err := kafkaReadArrayLength(reader)
		if err != nil {
			return nil, err
		}
		if topicError == 0 {
			availableTopics++
		}
		for partition := 0; partition < count; partition++ {
			var partitionError int16
			var id, leader int32
			if err := binary.Read(reader, binary.BigEndian, &partitionError); err != nil {
				return nil, err
			}
			if err := binary.Read(reader, binary.BigEndian, &id); err != nil {
				return nil, err
			}
			if err := binary.Read(reader, binary.BigEndian, &leader); err != nil {
				return nil, err
			}
			for range 2 {
				replicas, err := kafkaReadArrayLength(reader)
				if err != nil {
					return nil, err
				}
				for replica := 0; replica < replicas; replica++ {
					var brokerID int32
					if err := binary.Read(reader, binary.BigEndian, &brokerID); err != nil {
						return nil, err
					}
				}
			}
			if partitionError == 0 && topicError == 0 {
				partitions++
			}
		}
	}
	return map[string]float64{"brokers": float64(brokers), "topics": float64(availableTopics), "partitions": float64(partitions)}, nil
}

func kafkaWriteString(buffer *bytes.Buffer, value string) {
	_ = binary.Write(buffer, binary.BigEndian, int16(len(value)))
	_, _ = buffer.WriteString(value)
}

func kafkaReadString(reader *bytes.Reader) (string, error) {
	var length int16
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil || length < 0 {
		return "", errors.New("invalid Kafka string")
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return string(value), nil
}

func kafkaReadArrayLength(reader *bytes.Reader) (int, error) {
	var length int32
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil || length < 0 || length > 100000 {
		return 0, errors.New("invalid Kafka array")
	}
	return int(length), nil
}

func parseMetricNumber(value string) float64 {
	number, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return number
}
