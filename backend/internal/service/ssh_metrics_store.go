package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
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
		cpu_percent double NOT NULL, load1 double NOT NULL, load5 double NOT NULL DEFAULT 0, memory_percent double NOT NULL, disk_percent double NOT NULL,
		collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_ssh_metric_samples_source_time (source_id, collected_at)
	)`)
	if err != nil {
		return err
	}
	_, _ = current.Exec(`ALTER TABLE ssh_metric_samples ADD COLUMN load5 double NOT NULL DEFAULT 0 AFTER load1`)
	_, err = current.Exec(`CREATE TABLE IF NOT EXISTS host_filesystem_samples (
		id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
		source_id varchar(64) NOT NULL,
		filesystem varchar(512) NOT NULL,
		mount_point varchar(512) NOT NULL,
		total_bytes bigint NOT NULL,
		used_bytes bigint NOT NULL,
		used_percent double NOT NULL,
		collected_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_host_filesystem_samples_source_time (source_id, collected_at),
		INDEX idx_host_filesystem_samples_source_mount_time (source_id, mount_point, collected_at)
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
		if !ds.Enabled || (!strings.EqualFold(ds.Type, "ssh") && !isAgentSSH(ds)) {
			continue
		}
		_ = FillDataSourcePassword(&ds)
		metrics, err := collectHostDataSourceMetrics(ds)
		if err != nil {
			continue
		}
		collectedAt := time.Now()
		_, _ = current.Exec(`INSERT INTO ssh_metric_samples (source_id, cpu_percent, load1, load5, memory_percent, disk_percent, collected_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, ds.ID, metrics.cpu, metrics.load1, metrics.load5, metrics.memory, metrics.disk, collectedAt)
		_ = storeFilesystemMetrics(ds.ID, metrics.filesystems, collectedAt)
	}
}

func collectHostDataSourceMetrics(ds model.DataSource) (sshMetrics, error) {
	if isAgentSSH(ds) {
		metrics, err := collectAgentDataSourceMetrics(ds)
		return sshMetrics{cpu: metrics.cpu, load1: metrics.load1, load5: metrics.load5, memory: metrics.memory, disk: metrics.disk, filesystems: metrics.filesystems}, err
	}
	return collectSSHDataSourceMetrics(ds)
}

type sshMetrics struct {
	cpu, load1, load5, memory, disk float64
	filesystems                     []FilesystemMetric
}

func collectSSHDataSourceMetrics(ds model.DataSource) (sshMetrics, error) {
	out, err := executeSSHCommand(ds, "LC_ALL=C sh -c 'awk \"/^cpu / {print \\\u00242,\\\u00244}\" /proc/stat; awk \"{print \\\u00241,\\\u00242}\" /proc/loadavg; free -b | awk \"/^Mem:/ {print \\\u00242,\\\u00243}\"; df -PB1 / | awk \"NR==2 {print \\\u00243,\\\u00242}\"'")
	if err != nil {
		return sshMetrics{}, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 8 {
		return sshMetrics{}, fmt.Errorf("SSH 指标输出不完整")
	}
	values := make([]float64, 8)
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
	if values[4] > 0 {
		memory = (values[5] / values[4]) * 100
	}
	disk := 0.0
	if values[7] > 0 {
		disk = (values[6] / values[7]) * 100
	}
	filesystemRaw, err := executeSSHCommand(ds, "LC_ALL=C df -P -B1")
	if err != nil {
		return sshMetrics{}, err
	}
	return sshMetrics{cpu: cpu, load1: values[2], load5: values[3], memory: memory, disk: disk, filesystems: parseFilesystemMetrics(filesystemRaw)}, nil
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
	if isLocalSSH(ds) {
		return executeLocalShellCommand(command)
	}
	if strings.TrimSpace(ds.Password) == "" {
		return "", 0, errors.New("SSH 密码不能为空")
	}
	host := strings.TrimSpace(ds.Host)
	if err := validateOutboundAddress(host); err != nil {
		return "", 0, err
	}
	addr := net.JoinHostPort(host, strings.TrimSpace(ds.Port))
	// 传统 SSH 数据源不再要求主机指纹文件；Agent 数据源仍使用自身的令牌认证。
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

func executeLocalShellCommand(command string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	result := exec.CommandContext(ctx, "sh", "-c", command)
	out, err := result.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), 0, fmt.Errorf("本机命令执行超时")
	}
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return string(out), exitError.ExitCode(), nil
		}
		return string(out), 0, err
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
	var cpu, load1, load5, memory, disk float64
	var at time.Time
	err := current.QueryRow(`SELECT cpu_percent, load1, load5, memory_percent, disk_percent, collected_at FROM ssh_metric_samples WHERE source_id = ? ORDER BY collected_at DESC, id DESC LIMIT 1`, sourceID).Scan(&cpu, &load1, &load5, &memory, &disk, &at)
	if err != nil {
		return nil, time.Time{}, err
	}
	return map[string]float64{"cpu": cpu, "load1": load1, "load5": load5, "memory": memory, "disk": disk, "up": 1}, at, nil
}
