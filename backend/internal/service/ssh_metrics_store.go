package service

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"monitor-platform/internal/model"
)

func initSSHMetricStore() error {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return errors.New("metrics store is not initialized")
	}
	_, err := current.Exec(`CREATE TABLE IF NOT EXISTS ssh_metric_samples (
		id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
		source_id varchar(64) NOT NULL,
		cpu_percent double NOT NULL, load1 double NOT NULL, memory_percent double NOT NULL, disk_percent double NOT NULL,
		collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_ssh_metric_samples_source_time (source_id, collected_at)
	)`)
	return err
}

func startSSHMetricCollector() {
	go func() {
		collectAndStoreSSHMetrics()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			collectAndStoreSSHMetrics()
		}
	}()
}

func collectAndStoreSSHMetrics() {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return
	}
	for _, ds := range ListDataSources() {
		if !ds.Enabled || !strings.EqualFold(ds.Type, "ssh") {
			continue
		}
		_ = FillDataSourcePassword(&ds)
		metrics, err := collectSSHDataSourceMetrics(ds)
		if err != nil {
			continue
		}
		_, _ = current.Exec(`INSERT INTO ssh_metric_samples (source_id, cpu_percent, load1, memory_percent, disk_percent, collected_at) VALUES (?, ?, ?, ?, ?, ?)`, ds.ID, metrics.cpu, metrics.load1, metrics.memory, metrics.disk, time.Now())
	}
}

type sshMetrics struct{ cpu, load1, memory, disk float64 }

func collectSSHDataSourceMetrics(ds model.DataSource) (sshMetrics, error) {
	out, err := executeSSHCommand(ds, "LC_ALL=C sh -c 'awk \"/^cpu / {print \\\u00242,\\\u00244}\" /proc/stat; awk \"{print \\\u00241}\" /proc/loadavg; free -b | awk \"/^Mem:/ {print \\\u00242,\\\u00243}\"; df -PB1 / | awk \"NR==2 {print \\\u00243,\\\u00242}\"'")
	if err != nil {
		return sshMetrics{}, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 7 {
		return sshMetrics{}, fmt.Errorf("SSH 指标输出不完整")
	}
	values := make([]float64, 7)
	for i := range values {
		values[i], err = strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return sshMetrics{}, err
		}
	}
	cpuTotal := values[0] + values[1]
	cpu := 0.0
	if cpuTotal > 0 {
		cpu = (values[0] / cpuTotal) * 100
	}
	memory := 0.0
	if values[3] > 0 {
		memory = (values[4] / values[3]) * 100
	}
	disk := 0.0
	if values[6] > 0 {
		disk = (values[5] / values[6]) * 100
	}
	return sshMetrics{cpu: cpu, load1: values[2], memory: memory, disk: disk}, nil
}

func executeSSHCommand(ds model.DataSource, command string) (string, error) {
	out, exitCode, err := executeSSHCommandWithExitStatus(ds, command)
	if err != nil {
		return "", err
	}
	if exitCode != 0 {
		return "", fmt.Errorf("SSH 命令退出码为 %d", exitCode)
	}
	return out, nil
}

func executeSSHCommandWithExitStatus(ds model.DataSource, command string) (string, int, error) {
	if strings.TrimSpace(ds.Password) == "" {
		return "", 0, errors.New("SSH 密码不能为空")
	}
	addr := net.JoinHostPort(strings.TrimSpace(ds.Host), strings.TrimSpace(ds.Port))
	config := &ssh.ClientConfig{User: ds.Username, Auth: []ssh.AuthMethod{ssh.Password(ds.Password)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 8 * time.Second}
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return "", 0, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", 0, err
	}
	defer session.Close()
	out, err := session.CombinedOutput(command)
	if err != nil {
		if exitError, ok := err.(*ssh.ExitError); ok {
			return string(out), exitError.ExitStatus(), nil
		}
		return "", 0, err
	}
	return string(out), 0, nil
}

func LatestSSHDashboardMetrics(sourceID string) (map[string]float64, time.Time, error) {
	metricsMu.RLock()
	current := metricsDB
	metricsMu.RUnlock()
	if current == nil {
		return nil, time.Time{}, errors.New("metrics store is not initialized")
	}
	var cpu, load1, memory, disk float64
	var at time.Time
	err := current.QueryRow(`SELECT cpu_percent, load1, memory_percent, disk_percent, collected_at FROM ssh_metric_samples WHERE source_id = ? ORDER BY collected_at DESC, id DESC LIMIT 1`, sourceID).Scan(&cpu, &load1, &memory, &disk, &at)
	if err != nil {
		return nil, time.Time{}, err
	}
	return map[string]float64{"cpu": cpu, "load1": load1, "memory": memory, "disk": disk, "up": 1}, at, nil
}
