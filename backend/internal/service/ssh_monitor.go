package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"monitor-platform/internal/model"
)

const (
	sshMonitorInterval = time.Minute
	sshMonitorTimeout  = 12 * time.Second
	sshSnapshotLimit   = 5000
)

var sshMonitorOnce sync.Once

func initSSHMonitorStore(appDB *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS ssh_metric_snapshots (
			id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
			source_id varchar(64) NOT NULL,
			collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
			metrics_json json NOT NULL,
			INDEX idx_ssh_metric_source_time (source_id, collected_at)
		)`,
		`CREATE TABLE IF NOT EXISTS ssh_instance_status (
			source_id varchar(64) NOT NULL PRIMARY KEY,
			source_name varchar(120) NOT NULL,
			host varchar(255) NOT NULL,
			port varchar(16) NOT NULL,
			status varchar(32) NOT NULL,
			hostname varchar(255) NULL,
			kernel varchar(255) NULL,
			uptime_seconds bigint NOT NULL DEFAULT 0,
			cpu_usage_percent decimal(10,3) NOT NULL DEFAULT 0,
			load1 decimal(10,3) NOT NULL DEFAULT 0,
			load5 decimal(10,3) NOT NULL DEFAULT 0,
			load15 decimal(10,3) NOT NULL DEFAULT 0,
			memory_used bigint NOT NULL DEFAULT 0,
			memory_total bigint NOT NULL DEFAULT 0,
			memory_percent decimal(10,3) NOT NULL DEFAULT 0,
			disk_used bigint NOT NULL DEFAULT 0,
			disk_total bigint NOT NULL DEFAULT 0,
			disk_percent decimal(10,3) NOT NULL DEFAULT 0,
			process_count bigint NOT NULL DEFAULT 0,
			tcp_connections bigint NOT NULL DEFAULT 0,
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

func startSSHMonitorCollector() {
	sshMonitorOnce.Do(func() {
		go func() {
			collectAllSSHInstances()
			ticker := time.NewTicker(sshMonitorInterval)
			defer ticker.Stop()
			for range ticker.C {
				collectAllSSHInstances()
			}
		}()
	})
}

func collectAllSSHInstances() {
	appDB := currentStore()
	if appDB == nil {
		return
	}
	sources, err := listSSHDataSourcesWithSecrets(appDB)
	if err != nil {
		return
	}
	for _, ds := range sources {
		_ = collectSSHInstance(appDB, ds)
	}
	cleanupSSHMonitorData(appDB)
}

func listSSHDataSourcesWithSecrets(appDB *sql.DB) ([]model.DataSource, error) {
	rows, err := appDB.Query(`SELECT id, name, type, host, port, COALESCE(username, ''), COALESCE(password, '')
		FROM data_sources WHERE LOWER(type) = 'ssh' AND enabled = 1 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []model.DataSource
	for rows.Next() {
		var ds model.DataSource
		if err := rows.Scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Username, &ds.Password); err != nil {
			return nil, err
		}
		sources = append(sources, ds)
	}
	return sources, rows.Err()
}

func collectSSHInstance(appDB *sql.DB, ds model.DataSource) error {
	client, err := openSSHTarget(ds)
	if err != nil {
		saveSSHInstanceFailure(appDB, ds, err)
		return err
	}
	defer client.Close()
	output, err := runSSHCommand(client, sshMetricsCommand())
	if err != nil {
		saveSSHInstanceFailure(appDB, ds, err)
		return err
	}
	metrics := parseKeyValueMetrics(output)
	if err := saveSSHMetricSnapshot(appDB, ds.ID, metrics); err != nil {
		return err
	}
	return saveSSHInstanceStatus(appDB, ds, metrics, "健康", "")
}

func openSSHTarget(ds model.DataSource) (*ssh.Client, error) {
	if strings.TrimSpace(ds.Username) == "" {
		return nil, errors.New("SSH 用户名不能为空")
	}
	if strings.TrimSpace(ds.Password) == "" {
		return nil, errors.New("SSH 密码不能为空")
	}
	port := strings.TrimSpace(ds.Port)
	if port == "" {
		port = "22"
	}
	config := &ssh.ClientConfig{
		User:            ds.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(ds.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         8 * time.Second,
	}
	return ssh.Dial("tcp", ds.Host+":"+port, config)
}

func runSSHCommand(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Run(command); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return stdout.String(), nil
}

func sshMetricsCommand() string {
	return `sh -c '
hostname_value=$(hostname 2>/dev/null || echo unknown)
kernel_value=$(uname -sr 2>/dev/null || echo unknown)
uptime_seconds=$(awk "{print int(\$1)}" /proc/uptime 2>/dev/null || echo 0)
load_values=$(cat /proc/loadavg 2>/dev/null || echo "0 0 0")
set -- $load_values
load1=$1
load5=$2
load15=$3
cpu_a=$(awk "/^cpu /{print \$2+\$3+\$4+\$5+\$6+\$7+\$8, \$5}" /proc/stat 2>/dev/null)
sleep 1
cpu_b=$(awk "/^cpu /{print \$2+\$3+\$4+\$5+\$6+\$7+\$8, \$5}" /proc/stat 2>/dev/null)
cpu_usage_percent=$(awk -v a="$cpu_a" -v b="$cpu_b" "BEGIN{split(a,x,\" \"); split(b,y,\" \"); total=y[1]-x[1]; idle=y[2]-x[2]; if(total>0){printf \"%.2f\", (total-idle)*100/total}else{print \"0\"}}")
mem_total_kb=$(awk "/^MemTotal:/{print \$2}" /proc/meminfo 2>/dev/null || echo 0)
mem_available_kb=$(awk "/^MemAvailable:/{print \$2}" /proc/meminfo 2>/dev/null || echo 0)
memory_total=$((mem_total_kb * 1024))
memory_used=$(((mem_total_kb - mem_available_kb) * 1024))
memory_percent=$(awk -v used="$memory_used" -v total="$memory_total" "BEGIN{if(total>0){printf \"%.2f\", used*100/total}else{print \"0\"}}")
disk_line=$(df -P -B1 / 2>/dev/null | awk "NR==2{print \$2, \$3, \$5}")
set -- $disk_line
disk_total=${1:-0}
disk_used=${2:-0}
disk_percent=$(echo "${3:-0}" | tr -d "%")
process_count=$(ps -e --no-headers 2>/dev/null | wc -l | tr -d " ")
if command -v ss >/dev/null 2>&1; then
  tcp_connections=$(ss -tan state established 2>/dev/null | awk "NR>1{c++} END{print c+0}")
else
  tcp_connections=$(netstat -tan 2>/dev/null | awk "/ESTABLISHED/{c++} END{print c+0}")
fi
echo "hostname=$hostname_value"
echo "kernel=$kernel_value"
echo "uptime_seconds=$uptime_seconds"
echo "cpu_usage_percent=$cpu_usage_percent"
echo "load1=$load1"
echo "load5=$load5"
echo "load15=$load15"
echo "memory_total=$memory_total"
echo "memory_used=$memory_used"
echo "memory_percent=$memory_percent"
echo "disk_total=$disk_total"
echo "disk_used=$disk_used"
echo "disk_percent=$disk_percent"
echo "process_count=$process_count"
echo "tcp_connections=$tcp_connections"
'`
}

func parseKeyValueMetrics(raw string) map[string]string {
	metrics := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		metrics[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return metrics
}

func saveSSHMetricSnapshot(appDB *sql.DB, sourceID string, metrics map[string]string) error {
	payload, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = appDB.ExecContext(ctx, `INSERT INTO ssh_metric_snapshots (source_id, metrics_json) VALUES (?, ?)`, sourceID, string(payload))
	return err
}

func saveSSHInstanceStatus(appDB *sql.DB, ds model.DataSource, metrics map[string]string, status string, lastErr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := appDB.ExecContext(ctx, `INSERT INTO ssh_instance_status
		(source_id, source_name, host, port, status, hostname, kernel, uptime_seconds, cpu_usage_percent,
			load1, load5, load15, memory_used, memory_total, memory_percent, disk_used, disk_total,
			disk_percent, process_count, tcp_connections, last_error, last_collected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE source_name = VALUES(source_name), host = VALUES(host), port = VALUES(port),
			status = VALUES(status), hostname = VALUES(hostname), kernel = VALUES(kernel), uptime_seconds = VALUES(uptime_seconds),
			cpu_usage_percent = VALUES(cpu_usage_percent), load1 = VALUES(load1), load5 = VALUES(load5), load15 = VALUES(load15),
			memory_used = VALUES(memory_used), memory_total = VALUES(memory_total), memory_percent = VALUES(memory_percent),
			disk_used = VALUES(disk_used), disk_total = VALUES(disk_total), disk_percent = VALUES(disk_percent),
			process_count = VALUES(process_count), tcp_connections = VALUES(tcp_connections), last_error = VALUES(last_error),
			last_collected_at = VALUES(last_collected_at)`,
		ds.ID, ds.Name, ds.Host, ds.Port, status, metrics["hostname"], metrics["kernel"], sshMetricInt(metrics, "uptime_seconds"),
		sshMetricFloat(metrics, "cpu_usage_percent"), sshMetricFloat(metrics, "load1"), sshMetricFloat(metrics, "load5"),
		sshMetricFloat(metrics, "load15"), sshMetricInt(metrics, "memory_used"), sshMetricInt(metrics, "memory_total"),
		sshMetricFloat(metrics, "memory_percent"), sshMetricInt(metrics, "disk_used"), sshMetricInt(metrics, "disk_total"),
		sshMetricFloat(metrics, "disk_percent"), sshMetricInt(metrics, "process_count"), sshMetricInt(metrics, "tcp_connections"), lastErr)
	return err
}

func saveSSHInstanceFailure(appDB *sql.DB, ds model.DataSource, err error) {
	_ = saveSSHInstanceStatus(appDB, ds, map[string]string{}, "异常", err.Error())
}

func cleanupSSHMonitorData(appDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = appDB.ExecContext(ctx, `DELETE FROM ssh_metric_snapshots WHERE id NOT IN (
		SELECT id FROM (SELECT id FROM ssh_metric_snapshots ORDER BY collected_at DESC, id DESC LIMIT ?) keep_rows
	)`, sshSnapshotLimit)
}

func ListSSHInstanceStatuses() []model.SSHInstanceStatus {
	appDB := currentStore()
	if appDB == nil {
		return []model.SSHInstanceStatus{}
	}
	rows, err := appDB.Query(`SELECT source_id, source_name, host, port, status, COALESCE(hostname, ''), COALESCE(kernel, ''),
		uptime_seconds, cpu_usage_percent, load1, load5, load15, memory_used, memory_total, memory_percent,
		disk_used, disk_total, disk_percent, process_count, tcp_connections, COALESCE(last_error, ''), last_collected_at
		FROM ssh_instance_status ORDER BY last_collected_at DESC`)
	if err != nil {
		return []model.SSHInstanceStatus{}
	}
	defer rows.Close()
	items := []model.SSHInstanceStatus{}
	for rows.Next() {
		var item model.SSHInstanceStatus
		if err := rows.Scan(&item.SourceID, &item.SourceName, &item.Host, &item.Port, &item.Status, &item.Hostname, &item.Kernel,
			&item.UptimeSeconds, &item.CPUUsagePercent, &item.Load1, &item.Load5, &item.Load15, &item.MemoryUsed,
			&item.MemoryTotal, &item.MemoryPercent, &item.DiskUsed, &item.DiskTotal, &item.DiskPercent,
			&item.ProcessCount, &item.TCPConnections, &item.LastError, &item.LastCollectedAt); err != nil {
			continue
		}
		items = append(items, item)
	}
	return items
}

func ListSSHMetricSnapshots(sourceID string, limit int, start *time.Time, end *time.Time) []model.SSHMetricSnapshot {
	appDB := currentStore()
	if appDB == nil {
		return []model.SSHMetricSnapshot{}
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
	rows, err := appDB.Query(`SELECT id, source_id, collected_at, metrics_json FROM ssh_metric_snapshots
		WHERE `+strings.Join(conditions, " AND ")+` ORDER BY collected_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return []model.SSHMetricSnapshot{}
	}
	defer rows.Close()
	items := []model.SSHMetricSnapshot{}
	for rows.Next() {
		var item model.SSHMetricSnapshot
		var raw string
		if err := rows.Scan(&item.ID, &item.SourceID, &item.CollectedAt, &raw); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(raw), &item.Metrics)
		items = append(items, item)
	}
	return items
}

func sshMetricInt(metrics map[string]string, key string) int64 {
	return parseInt64(metrics[key])
}

func sshMetricFloat(metrics map[string]string, key string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(metrics[key]), 64)
	if err != nil {
		return 0
	}
	return value
}

func sshMetricSchema() map[string]map[string][]string {
	return map[string]map[string][]string{
		"system": {
			"SSH 关键性能指标": {
				"cpu_usage_percent",
				"load1",
				"load5",
				"load15",
				"memory_percent",
				"disk_percent",
				"process_count",
				"tcp_connections",
				"uptime_seconds",
			},
		},
	}
}
