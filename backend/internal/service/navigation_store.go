package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

func initNavigationStore(current *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_links (
		id varchar(64) PRIMARY KEY,
		name varchar(120) NOT NULL,
		url varchar(2048) NOT NULL,
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}
	_, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS hadoop_menu_items (
		source_id varchar(64) PRIMARY KEY,
		name varchar(120) NOT NULL DEFAULT '',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return err
	}
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ambari_menu_items (
		source_id varchar(64) PRIMARY KEY,
		name varchar(120) NOT NULL DEFAULT '',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS mysql_menu_items (source_id varchar(64) PRIMARY KEY, name varchar(120) NOT NULL DEFAULT '', created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS consul_menu_items (source_id varchar(64) PRIMARY KEY, name varchar(120) NOT NULL DEFAULT '', created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS hive_menu_items (source_id varchar(64) PRIMARY KEY, name varchar(120) NOT NULL DEFAULT '', created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS postgresql_menu_items (source_id varchar(64) PRIMARY KEY, name varchar(120) NOT NULL DEFAULT '', created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS mysql_report_tasks (
		id varchar(64) PRIMARY KEY,
		name varchar(120) NOT NULL,
		source_id varchar(64) NOT NULL,
		sql_text mediumtext NOT NULL,
		time_range text NOT NULL,
		schedule varchar(120) NOT NULL DEFAULT '手动',
		enabled tinyint(1) NOT NULL DEFAULT 1,
		status varchar(24) NOT NULL DEFAULT '待执行',
		last_run_at datetime NULL,
		last_message varchar(255) NOT NULL DEFAULT '',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		INDEX idx_mysql_report_source (source_id),
		INDEX idx_mysql_report_updated (updated_at)
	)`); err != nil {
		return err
	}
	for _, column := range []struct{ name, want, definition string }{
		{"sql_text", "mediumtext", "MEDIUMTEXT NOT NULL"},
		{"time_range", "text", "TEXT NOT NULL"},
	} {
		var dataType string
		if err := current.QueryRowContext(ctx, `SELECT DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mysql_report_tasks' AND COLUMN_NAME = ?`, column.name).Scan(&dataType); err != nil {
			return err
		}
		if dataType != column.want {
			if _, err := current.ExecContext(ctx, "ALTER TABLE mysql_report_tasks MODIFY COLUMN "+column.name+" "+column.definition); err != nil {
				return err
			}
		}
	}
	_, _ = current.ExecContext(ctx, `ALTER TABLE mysql_report_tasks ADD COLUMN alert_enabled tinyint(1) NOT NULL DEFAULT 0`)
	_, _ = current.ExecContext(ctx, `ALTER TABLE mysql_report_tasks ADD COLUMN alert_compare varchar(24) NOT NULL DEFAULT ''`)
	_, _ = current.ExecContext(ctx, `ALTER TABLE mysql_report_tasks ADD COLUMN alert_operator varchar(8) NOT NULL DEFAULT ''`)
	_, _ = current.ExecContext(ctx, `ALTER TABLE mysql_report_tasks ADD COLUMN alert_threshold double NOT NULL DEFAULT 0`)
	_, _ = current.ExecContext(ctx, `ALTER TABLE mysql_report_tasks ADD COLUMN alert_unit varchar(8) NOT NULL DEFAULT ''`)
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ssh_custom_commands (
		id varchar(64) PRIMARY KEY,
		label varchar(120) NOT NULL,
		command varchar(2000) NOT NULL,
		category varchar(60) NOT NULL DEFAULT '自定义',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}

	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ssh_command_history (
		id bigint AUTO_INCREMENT PRIMARY KEY,
		source_id varchar(64) NOT NULL DEFAULT '',
		source_name varchar(200) NOT NULL DEFAULT '',
		command varchar(2000) NOT NULL,
		exit_code int NOT NULL DEFAULT 0,
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_history_source (source_id),
		INDEX idx_history_time (created_at)
	)`); err != nil {
		return err
	}

	builtins := []struct{ id, label, cmd, category string }{
		// 系统信息
		{"builtin-hostname", "主机名", "hostname", "系统信息"},
		{"builtin-os-release", "系统版本", "cat /etc/os-release | head -5", "系统信息"},
		{"builtin-uname", "内核版本", "uname -a", "系统信息"},
		{"builtin-uptime", "运行时间与负载", "uptime", "系统信息"},
		{"builtin-whoami", "当前用户", "whoami && id", "系统信息"},
		{"builtin-date", "系统时间", "timedatectl", "系统信息"},
		{"builtin-last-reboot", "最近重启记录", "last reboot | head -5", "系统信息"},

		// 资源使用
		{"builtin-nproc", "CPU 核心数", "nproc", "资源使用"},
		{"builtin-cpu-load", "CPU 实时负载", "top -bn1 | head -20", "资源使用"},
		{"builtin-free", "内存使用", "free -h", "资源使用"},
		{"builtin-df", "磁盘使用", "df -h", "资源使用"},
		{"builtin-df-inode", "磁盘 inode", "df -i", "资源使用"},
		{"builtin-du-top", "目录占用 Top10", "du -sh /* 2>/dev/null | sort -rh | head -10", "资源使用"},
		{"builtin-ps-cpu", "Top CPU 进程", "ps aux --sort=-%cpu | head -10", "资源使用"},
		{"builtin-ps-mem", "Top 内存进程", "ps aux --sort=-%mem | head -10", "资源使用"},
		{"builtin-iostat", "磁盘 IO", "iostat -x 1 2 2>/dev/null || vmstat 1 3", "资源使用"},

		// 网络排查
		{"builtin-ip-addr", "IP 地址", "ip addr | grep \"inet \"", "网络"},
		{"builtin-ss-listen", "监听端口", "ss -tlnp", "网络"},
		{"builtin-ss-stat", "连接状态统计", "ss -s", "网络"},
		{"builtin-ss-estab", "ESTABLISHED 连接", "ss -tnp state established | head -30", "网络"},
		{"builtin-ip-route", "路由表", "ip route", "网络"},
		{"builtin-dns", "DNS 解析", "cat /etc/resolv.conf", "网络"},
		{"builtin-ping-gw", "Ping 网关", "ip route | grep default | awk '{print $3}' | head -1 | xargs -I{} ping -c 3 {}", "网络"},
		{"builtin-firewall", "防火墙规则", "iptables -L -n 2>/dev/null || firewall-cmd --list-all 2>/dev/null", "网络"},

		// 服务排查
		{"builtin-systemd-running", "运行中的服务", "systemctl list-units --type=service --state=running", "服务"},
		{"builtin-systemd-failed", "失败的服务", "systemctl --failed --no-pager", "服务"},
		{"builtin-journal", "最近系统日志", "journalctl -n 30 --no-pager", "服务"},
		{"builtin-journal-err", "最近错误日志", "journalctl -p err -n 20 --no-pager", "服务"},
		{"builtin-docker", "Docker 容器", "docker ps", "服务"},
		{"builtin-docker-stopped", "停止的容器", "docker ps -a --filter status=exited", "服务"},
		{"builtin-port-80", "80/443 端口占用", "ss -tlnp | grep -E ':80|:443'", "服务"},

		// Kubernetes
		{"builtin-k8s-nodes", "节点列表", "kubectl get nodes -o wide", "Kubernetes"},
		{"builtin-k8s-ns", "命名空间", "kubectl get ns", "Kubernetes"},
		{"builtin-k8s-pods", "全部 Pod", "kubectl get pods -A -o wide", "Kubernetes"},
		{"builtin-k8s-bad-pods", "异常 Pod", "kubectl get pods -A | grep -v Running", "Kubernetes"},
		{"builtin-k8s-top-nodes", "节点资源", "kubectl top nodes", "Kubernetes"},
		{"builtin-k8s-top-pods", "Pod 资源", "kubectl top pods -A --sort-by=cpu | head -20", "Kubernetes"},
		{"builtin-k8s-deploy", "Deployment", "kubectl get deploy -A", "Kubernetes"},
		{"builtin-k8s-events", "最近事件", "kubectl get events -A --sort-by=.lastTimestamp | tail -20", "Kubernetes"},
		{"builtin-k8s-desc-pod", "查看 Pod 详情", "kubectl describe pod <pod名> -n <命名空间>", "Kubernetes"},
		{"builtin-k8s-logs", "查看 Pod 日志", "kubectl logs <pod名> -n <命名空间> --tail=50", "Kubernetes"},

		// 异常排查
		{"builtin-oom", "OOM 记录", "dmesg | grep -i 'oom\\|out of memory' | tail -10", "异常排查"},
		{"builtin-disk-full", "大文件查找", "find / -xdev -type f -size +500M 2>/dev/null | head -20", "异常排查"},
		{"builtin-inode-full", "小文件目录", "find / -xdev -type d -size +1000k 2>/dev/null | head -10", "异常排查"},
		{"builtin-tcp-tw", "TIME_WAIT 统计", "ss -ant | awk '{print $1}' | sort | uniq -c | sort -rn", "异常排查"},
		{"builtin-conn-refused", "连接拒绝排查", "ss -tn state time-wait | wc -l", "异常排查"},
		{"builtin-loadavg", "系统负载详情", "cat /proc/loadavg", "异常排查"},
		{"builtin-mem-info", "内存详情", "cat /proc/meminfo | head -20", "异常排查"},
		{"builtin-dmesg-err", "内核错误日志", "dmesg --level=err,warn | tail -20", "异常排查"},
		{"builtin-auth-fail", "登录失败记录", "lastb 2>/dev/null | head -10 || journalctl -u sshd -n 20 --no-pager", "异常排查"},
		{"builtin-cron", "定时任务", "crontab -l 2>/dev/null; ls -la /etc/cron.d/", "异常排查"},
		{"builtin-lsof-port", "查端口占用", "lsof -i:端口号", "异常排查"},
		{"builtin-lsof-pid", "查进程文件句柄", "lsof -p PID", "异常排查"},
		{"builtin-ntp", "时间同步状态", "chronyc sources 2>/dev/null || ntpq -p 2>/dev/null", "异常排查"},
		{"builtin-messages-log", "系统消息日志", "tail -100 /var/log/messages", "异常排查"},
		{"builtin-secure-log", "安全日志", "tail -100 /var/log/secure", "异常排查"},
		{"builtin-tcpdump", "抓包排查", "tcpdump -i eth0 port 80 -c 20", "异常排查"},
		{"builtin-conn-rank", "来源 IP 连接数排行", "ss -tn | awk '{print $5}' | cut -d: -f1 | sort | uniq -c | sort -rn | head -10", "异常排查"},
		{"builtin-zombie", "僵尸进程数", "ps aux | grep defunct | wc -l", "异常排查"},
		{"builtin-k8s-notready", "NotReady 节点", "kubectl get nodes | grep -v Ready", "Kubernetes"},
		{"builtin-k8s-restart", "Pod 重启次数排行", "kubectl get pods -A | awk '$5>0'", "Kubernetes"},
		{"builtin-docker-logs", "容器日志", "docker logs --tail 50 容器名", "服务"},
		{"builtin-docker-stats", "容器资源占用", "docker stats --no-stream", "服务"},
	}
	for _, b := range builtins {
		current.ExecContext(ctx, `INSERT IGNORE INTO ssh_custom_commands (id, label, command, category) VALUES (?, ?, ?, ?)`, b.id, b.label, b.cmd, b.category)
	}

	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS knowledge_docs (
		id varchar(64) PRIMARY KEY,
		title varchar(200) NOT NULL,
		content longtext NOT NULL,
		source_name varchar(200) NOT NULL DEFAULT '',
		category varchar(100) NOT NULL DEFAULT '基础',
		created_by varchar(64) NOT NULL DEFAULT '',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		INDEX idx_knowledge_updated (updated_at)
	)`); err != nil {
		return err
	}
	// 兼容已有表：补 category 字段
	_, _ = current.ExecContext(ctx, `ALTER TABLE knowledge_docs ADD COLUMN category varchar(100) NOT NULL DEFAULT '基础'`)
	_, _ = current.ExecContext(ctx, `ALTER TABLE knowledge_docs ADD INDEX idx_knowledge_category (category)`)
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS hadoop_application_snapshots (
		source_id varchar(64) NOT NULL,
		application_id varchar(160) NOT NULL,
		application_json json NOT NULL,
		first_seen_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_seen_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (source_id, application_id),
		INDEX idx_hadoop_snapshot_last_seen (source_id, last_seen_at)
	)`); err != nil {
		return err
	}
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS hadoop_container_snapshots (
		source_id varchar(64) NOT NULL,
		application_id varchar(160) NOT NULL,
		container_json json NOT NULL,
		first_seen_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_seen_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (source_id, application_id),
		INDEX idx_hadoop_container_snapshot_last_seen (source_id, last_seen_at)
	)`); err != nil {
		return err
	}
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS hadoop_health_checks (
		source_id varchar(64) PRIMARY KEY,
		resource_manager varchar(32) NOT NULL DEFAULT '未检测',
		node_manager varchar(32) NOT NULL DEFAULT '未配置',
		job_history varchar(32) NOT NULL DEFAULT '未配置',
		details text NOT NULL,
		checked_at datetime NOT NULL
	)`); err != nil {
		return err
	}
	// Existing installations predate menu naming. Ignore duplicate-column errors.
	_, _ = current.ExecContext(ctx, `ALTER TABLE hadoop_menu_items ADD COLUMN name varchar(120) NOT NULL DEFAULT '' AFTER source_id`)
	if _, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS dashboard_items (
		id varchar(64) PRIMARY KEY,
		name varchar(120) NOT NULL,
		source_id varchar(64) NOT NULL,
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		UNIQUE KEY uq_dashboard_items_source (source_id),
		INDEX idx_dashboard_items_created_at (created_at)
	)`); err != nil {
		return err
	}
	_, err = current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS system_settings (
		setting_key varchar(120) PRIMARY KEY,
		setting_value varchar(255) NOT NULL,
		updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`)
	return err
}

func ListPostgreSQLMenuItems() []model.PostgreSQLMenuItem {
	current := currentStore()
	if current == nil {
		return []model.PostgreSQLMenuItem{}
	}
	rows, err := current.Query(`SELECT source_id, name FROM postgresql_menu_items ORDER BY created_at, source_id`)
	if err != nil {
		return []model.PostgreSQLMenuItem{}
	}
	defer rows.Close()
	items := []model.PostgreSQLMenuItem{}
	for rows.Next() {
		var item model.PostgreSQLMenuItem
		if rows.Scan(&item.SourceID, &item.Name) == nil {
			items = append(items, item)
		}
	}
	return items
}

func AddPostgreSQLMenuItem(sourceID, name string) (model.PostgreSQLMenuItem, error) {
	sourceID, name = strings.TrimSpace(sourceID), strings.TrimSpace(name)
	source, err := GetDataSourceByID(sourceID)
	if err != nil || !(strings.EqualFold(source.Type, "postgresql") || strings.EqualFold(source.Type, "postgres")) {
		return model.PostgreSQLMenuItem{}, errors.New("PostgreSQL data source not found")
	}
	if name == "" {
		name = source.Name
	}
	if len(name) > 120 {
		return model.PostgreSQLMenuItem{}, errors.New("PostgreSQL menu name must be at most 120 characters")
	}
	current := currentStore()
	if current == nil {
		return model.PostgreSQLMenuItem{}, errors.New("PostgreSQL menu store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `INSERT INTO postgresql_menu_items (source_id, name) VALUES (?, ?) ON DUPLICATE KEY UPDATE name = VALUES(name)`, sourceID, name); err != nil {
		return model.PostgreSQLMenuItem{}, err
	}
	return model.PostgreSQLMenuItem{SourceID: sourceID, Name: name}, nil
}

func ListHiveMenuItems() []model.HiveMenuItem {
	current := currentStore()
	if current == nil {
		return []model.HiveMenuItem{}
	}
	rows, err := current.Query(`SELECT source_id, name FROM hive_menu_items ORDER BY created_at, source_id`)
	if err != nil {
		return []model.HiveMenuItem{}
	}
	defer rows.Close()
	items := []model.HiveMenuItem{}
	for rows.Next() {
		var item model.HiveMenuItem
		if rows.Scan(&item.SourceID, &item.Name) == nil {
			items = append(items, item)
		}
	}
	return items
}

func AddHiveMenuItem(sourceID, name string) (model.HiveMenuItem, error) {
	sourceID, name = strings.TrimSpace(sourceID), strings.TrimSpace(name)
	source, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(source.Type, "hive") {
		return model.HiveMenuItem{}, errors.New("Hive 数据源不存在")
	}
	if name == "" {
		name = source.Name
	}
	if len(name) > 120 {
		return model.HiveMenuItem{}, errors.New("Hive 菜单名称不能超过 120 个字符")
	}
	current := currentStore()
	if current == nil {
		return model.HiveMenuItem{}, errors.New("Hive 菜单存储未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `INSERT INTO hive_menu_items (source_id, name) VALUES (?, ?) ON DUPLICATE KEY UPDATE name = VALUES(name)`, sourceID, name); err != nil {
		return model.HiveMenuItem{}, err
	}
	return model.HiveMenuItem{SourceID: sourceID, Name: name}, nil
}

func ListPlatformLinks() []model.PlatformLink {
	current := currentStore()
	if current == nil {
		return []model.PlatformLink{}
	}
	rows, err := current.Query(`SELECT id, name, url FROM platform_links ORDER BY created_at, id`)
	if err != nil {
		return []model.PlatformLink{}
	}
	defer rows.Close()

	items := make([]model.PlatformLink, 0)
	for rows.Next() {
		var item model.PlatformLink
		if err := rows.Scan(&item.ID, &item.Name, &item.URL); err == nil {
			items = append(items, item)
		}
	}
	return items
}

func ReplacePlatformLinks(items []model.PlatformLink) ([]model.PlatformLink, error) {
	current := currentStore()
	if current == nil {
		return nil, errors.New("platform link store is not initialized")
	}
	if len(items) > 100 {
		return nil, errors.New("at most 100 platform links are allowed")
	}

	normalized := make([]model.PlatformLink, 0, len(items))
	for index, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Name = strings.TrimSpace(item.Name)
		item.URL = strings.TrimSpace(item.URL)
		if item.ID == "" {
			item.ID = fmt.Sprintf("platform-%d-%d", time.Now().UnixNano(), index)
		}
		if item.Name == "" || len(item.Name) > 120 {
			return nil, errors.New("platform name must be 1 to 120 characters")
		}
		parsed, err := url.ParseRequestURI(item.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, errors.New("platform URL must use http or https")
		}
		if len(item.URL) > 2048 {
			return nil, errors.New("platform URL is too long")
		}
		normalized = append(normalized, item)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := current.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM platform_links`); err != nil {
		return nil, err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO platform_links (id, name, url) VALUES (?, ?, ?)`)
	if err != nil {
		return nil, err
	}
	defer statement.Close()
	for _, item := range normalized {
		if _, err := statement.ExecContext(ctx, item.ID, item.Name, item.URL); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return normalized, nil
}

func ListHadoopMenuItems() []model.HadoopMenuItem {
	current := currentStore()
	if current == nil {
		return []model.HadoopMenuItem{}
	}
	rows, err := current.Query(`SELECT source_id, name FROM hadoop_menu_items ORDER BY created_at, source_id`)
	if err != nil {
		return []model.HadoopMenuItem{}
	}
	defer rows.Close()

	items := make([]model.HadoopMenuItem, 0)
	for rows.Next() {
		var item model.HadoopMenuItem
		if err := rows.Scan(&item.SourceID, &item.Name); err == nil {
			items = append(items, item)
		}
	}
	return items
}

func AddHadoopMenuItem(sourceID string, name string) (model.HadoopMenuItem, error) {
	sourceID = strings.TrimSpace(sourceID)
	name = strings.TrimSpace(name)
	if sourceID == "" {
		return model.HadoopMenuItem{}, errors.New("Hadoop source ID is required")
	}
	source, err := GetDataSourceByID(sourceID)
	if err != nil {
		return model.HadoopMenuItem{}, errors.New("Hadoop data source not found")
	}
	if !strings.EqualFold(source.Type, "hadoop") {
		return model.HadoopMenuItem{}, errors.New("only Hadoop data sources can be imported into the menu")
	}
	if name == "" {
		name = source.Name
	}
	if len(name) > 120 {
		return model.HadoopMenuItem{}, errors.New("Hadoop menu name must be at most 120 characters")
	}
	current := currentStore()
	if current == nil {
		return model.HadoopMenuItem{}, errors.New("Hadoop menu store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `INSERT INTO hadoop_menu_items (source_id, name) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE name = VALUES(name)`, sourceID, name); err != nil {
		return model.HadoopMenuItem{}, err
	}
	return model.HadoopMenuItem{SourceID: sourceID, Name: name}, nil
}

func DeleteHadoopMenuItem(sourceID string) error {
	current := currentStore()
	if current == nil {
		return errors.New("Hadoop menu store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := current.ExecContext(ctx, `DELETE FROM hadoop_menu_items WHERE source_id = ?`, strings.TrimSpace(sourceID))
	return err
}

func ListAmbariMenuItems() []model.AmbariMenuItem {
	current := currentStore()
	if current == nil {
		return []model.AmbariMenuItem{}
	}
	rows, err := current.Query(`SELECT source_id, name FROM ambari_menu_items ORDER BY created_at, source_id`)
	if err != nil {
		return []model.AmbariMenuItem{}
	}
	defer rows.Close()
	items := make([]model.AmbariMenuItem, 0)
	for rows.Next() {
		var item model.AmbariMenuItem
		if rows.Scan(&item.SourceID, &item.Name) == nil {
			items = append(items, item)
		}
	}
	return items
}

func AddAmbariMenuItem(sourceID, name string) (model.AmbariMenuItem, error) {
	sourceID, name = strings.TrimSpace(sourceID), strings.TrimSpace(name)
	if sourceID == "" {
		return model.AmbariMenuItem{}, errors.New("Ambari source ID is required")
	}
	source, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(source.Type, "ambari") {
		return model.AmbariMenuItem{}, errors.New("Ambari data source not found")
	}
	if name == "" {
		name = source.Name
	}
	if len(name) > 120 {
		return model.AmbariMenuItem{}, errors.New("Ambari menu name must be at most 120 characters")
	}
	current := currentStore()
	if current == nil {
		return model.AmbariMenuItem{}, errors.New("Ambari menu store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `INSERT INTO ambari_menu_items (source_id, name) VALUES (?, ?) ON DUPLICATE KEY UPDATE name = VALUES(name)`, sourceID, name); err != nil {
		return model.AmbariMenuItem{}, err
	}
	return model.AmbariMenuItem{SourceID: sourceID, Name: name}, nil
}

func DeleteAmbariMenuItem(sourceID string) error {
	current := currentStore()
	if current == nil {
		return errors.New("Ambari menu store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := current.ExecContext(ctx, `DELETE FROM ambari_menu_items WHERE source_id = ?`, strings.TrimSpace(sourceID))
	return err
}

func ListMySQLMenuItems() []model.MySQLMenuItem {
	current := currentStore()
	if current == nil {
		return []model.MySQLMenuItem{}
	}
	rows, err := current.Query(`SELECT source_id, name FROM mysql_menu_items ORDER BY created_at, source_id`)
	if err != nil {
		return []model.MySQLMenuItem{}
	}
	defer rows.Close()
	items := []model.MySQLMenuItem{}
	for rows.Next() {
		var item model.MySQLMenuItem
		if rows.Scan(&item.SourceID, &item.Name) == nil {
			items = append(items, item)
		}
	}
	return items
}
func AddMySQLMenuItem(sourceID, name string) (model.MySQLMenuItem, error) {
	sourceID, name = strings.TrimSpace(sourceID), strings.TrimSpace(name)
	source, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(source.Type, "mysql") {
		return model.MySQLMenuItem{}, errors.New("MySQL data source not found")
	}
	if name == "" {
		name = source.Name
	}
	if len(name) > 120 {
		return model.MySQLMenuItem{}, errors.New("MySQL menu name must be at most 120 characters")
	}
	current := currentStore()
	if current == nil {
		return model.MySQLMenuItem{}, errors.New("MySQL menu store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `INSERT INTO mysql_menu_items (source_id, name) VALUES (?, ?) ON DUPLICATE KEY UPDATE name = VALUES(name)`, sourceID, name); err != nil {
		return model.MySQLMenuItem{}, err
	}
	return model.MySQLMenuItem{SourceID: sourceID, Name: name}, nil
}

func ListConsulMenuItems() []model.ConsulMenuItem {
	current := currentStore()
	if current == nil {
		return []model.ConsulMenuItem{}
	}
	rows, err := current.Query(`SELECT source_id, name FROM consul_menu_items ORDER BY created_at, source_id`)
	if err != nil {
		return []model.ConsulMenuItem{}
	}
	defer rows.Close()
	items := []model.ConsulMenuItem{}
	for rows.Next() {
		var item model.ConsulMenuItem
		if rows.Scan(&item.SourceID, &item.Name) == nil {
			items = append(items, item)
		}
	}
	return items
}

func AddConsulMenuItem(sourceID, name string) (model.ConsulMenuItem, error) {
	sourceID, name = strings.TrimSpace(sourceID), strings.TrimSpace(name)
	source, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(source.Type, "consul") {
		return model.ConsulMenuItem{}, errors.New("Consul data source not found")
	}
	if name == "" {
		name = source.Name
	}
	if len(name) > 120 {
		return model.ConsulMenuItem{}, errors.New("Consul menu name must be at most 120 characters")
	}
	current := currentStore()
	if current == nil {
		return model.ConsulMenuItem{}, errors.New("Consul menu store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `INSERT INTO consul_menu_items (source_id, name) VALUES (?, ?) ON DUPLICATE KEY UPDATE name = VALUES(name)`, sourceID, name); err != nil {
		return model.ConsulMenuItem{}, err
	}
	return model.ConsulMenuItem{SourceID: sourceID, Name: name}, nil
}

func ListDashboardItems() []model.DashboardItem {
	current := currentStore()
	if current == nil {
		return []model.DashboardItem{}
	}
	rows, err := current.Query(`SELECT dashboard.id, dashboard.name, dashboard.source_id, source.name, source.type, dashboard.created_at
		FROM dashboard_items dashboard
		INNER JOIN data_sources source ON source.id = dashboard.source_id
		WHERE source.type IN ('MySQL', 'SSH', 'Agent', 'Redis', 'ClickHouse', 'Kafka')
		ORDER BY CASE source.type WHEN 'SSH' THEN 0 WHEN 'Agent' THEN 0 WHEN 'MySQL' THEN 1 WHEN 'Redis' THEN 2 WHEN 'ClickHouse' THEN 3 WHEN 'Kafka' THEN 4 ELSE 9 END, source.name ASC, dashboard.name ASC, dashboard.id ASC`)
	if err != nil {
		return []model.DashboardItem{}
	}
	defer rows.Close()
	items := make([]model.DashboardItem, 0)
	for rows.Next() {
		var item model.DashboardItem
		if err := rows.Scan(&item.ID, &item.Name, &item.SourceID, &item.SourceName, &item.SourceType, &item.CreatedAt); err == nil {
			items = append(items, item)
		}
	}
	return items
}

func AddDashboardItem(item model.DashboardItem) (model.DashboardItem, error) {
	item.SourceID = strings.TrimSpace(item.SourceID)
	item.Name = strings.TrimSpace(item.Name)
	if item.SourceID == "" {
		return model.DashboardItem{}, errors.New("dashboard source ID is required")
	}
	source, err := GetDataSourceByID(item.SourceID)
	if err != nil {
		return model.DashboardItem{}, errors.New("dashboard data source not found")
	}
	if !strings.EqualFold(source.Type, "mysql") && !strings.EqualFold(source.Type, "ssh") && !strings.EqualFold(source.Type, "agent") && !strings.EqualFold(source.Type, "redis") && !strings.EqualFold(source.Type, "clickhouse") && !strings.EqualFold(source.Type, "kafka") {
		return model.DashboardItem{}, errors.New("only MySQL, SSH, Redis, ClickHouse and Kafka data sources support dashboards")
	}
	if item.ID == "" {
		item.ID = fmt.Sprintf("dashboard-%d", time.Now().UnixNano())
	}
	if item.Name == "" {
		item.Name = source.Name + " 大屏"
	}
	if len(item.Name) > 120 {
		return model.DashboardItem{}, errors.New("dashboard name must be at most 120 characters")
	}
	current := currentStore()
	if current == nil {
		return model.DashboardItem{}, errors.New("dashboard store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := current.ExecContext(ctx, `INSERT INTO dashboard_items (id, name, source_id) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE name = VALUES(name), updated_at = CURRENT_TIMESTAMP`, item.ID, item.Name, item.SourceID); err != nil {
		return model.DashboardItem{}, err
	}
	for _, saved := range ListDashboardItems() {
		if saved.SourceID == item.SourceID {
			return saved, nil
		}
	}
	return model.DashboardItem{}, errors.New("dashboard item could not be loaded")
}

func DeleteDashboardItem(id string) error {
	current := currentStore()
	if current == nil {
		return errors.New("dashboard store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := current.ExecContext(ctx, `DELETE FROM dashboard_items WHERE id = ?`, strings.TrimSpace(id))
	return err
}

func refreshSettingDefinition(scope string) (string, model.RefreshSettings) {
	switch strings.TrimSpace(scope) {
	case "datasource":
		return "data_source_health_interval", model.RefreshSettings{Value: 2, Unit: "m"}
	case "hadoop":
		return "hadoop_task_list_interval", model.RefreshSettings{Value: 5, Unit: "m"}
	case "dashboard":
		return "dashboard_refresh_interval", model.RefreshSettings{Value: 1, Unit: "m"}
	case "consul":
		return "consul_kv_sync_interval", model.RefreshSettings{Value: 1, Unit: "m"}
	case "mysql":
		return "mysql_metric_sync_interval", model.RefreshSettings{Value: 30, Unit: "s"}
	case "prometheus":
		return "prometheus_alert_sync_interval", model.RefreshSettings{Value: 15, Unit: "s"}
	default:
		return "refresh_interval", model.RefreshSettings{Value: 15, Unit: "s"}
	}
}

func readRefreshSetting(current *sql.DB, settingKey string) (model.RefreshSettings, bool) {
	var raw string
	if err := current.QueryRow(`SELECT setting_value FROM system_settings WHERE setting_key = ?`, settingKey).Scan(&raw); err != nil {
		return model.RefreshSettings{}, false
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return model.RefreshSettings{}, false
	}
	value, err := strconv.Atoi(parts[0])
	unit := parts[1]
	if err != nil || value < 1 || (unit != "s" && unit != "m" && unit != "h") {
		return model.RefreshSettings{}, false
	}
	return model.RefreshSettings{Value: value, Unit: unit, Configured: true}, true
}

func GetRefreshSettings(scopes ...string) model.RefreshSettings {
	scope := ""
	if len(scopes) > 0 {
		scope = strings.TrimSpace(scopes[0])
	}
	settingKey, settings := refreshSettingDefinition(scope)
	current := currentStore()
	if current == nil {
		return settings
	}
	if saved, ok := readRefreshSetting(current, settingKey); ok {
		return saved
	}
	// A module with no independent setting inherits the platform-wide frequency.
	if scope != "" && scope != "general" {
		if general, ok := readRefreshSetting(current, "refresh_interval"); ok {
			general.Configured = false
			return general
		}
	}
	return settings
}

func SaveRefreshSettings(settings model.RefreshSettings, scopes ...string) (model.RefreshSettings, error) {
	if settings.Value < 1 || settings.Value > 86400 {
		return model.RefreshSettings{}, errors.New("refresh value must be between 1 and 86400")
	}
	if settings.Unit != "s" && settings.Unit != "m" && settings.Unit != "h" {
		return model.RefreshSettings{}, errors.New("refresh unit must be s, m, or h")
	}
	current := currentStore()
	if current == nil {
		return model.RefreshSettings{}, errors.New("system settings store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scope := ""
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	settingKey, _ := refreshSettingDefinition(scope)
	if _, err := current.ExecContext(ctx, `INSERT INTO system_settings (setting_key, setting_value) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value), updated_at = CURRENT_TIMESTAMP`, settingKey, fmt.Sprintf("%d:%s", settings.Value, settings.Unit)); err != nil {
		return model.RefreshSettings{}, err
	}
	settings.Configured = true
	return settings, nil
}

// DeleteRefreshSettings removes a module-specific override so it follows the
// common refresh interval again. The common interval itself must always remain.
func DeleteRefreshSettings(scopes ...string) error {
	scope := ""
	if len(scopes) > 0 {
		scope = strings.TrimSpace(scopes[0])
	}
	if scope == "" || scope == "general" {
		return errors.New("通用刷新频率不能删除")
	}
	settingKey, _ := refreshSettingDefinition(scope)
	if settingKey == "refresh_interval" {
		return errors.New("不支持的刷新频率类型")
	}
	current := currentStore()
	if current == nil {
		return errors.New("system settings store is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := current.ExecContext(ctx, `DELETE FROM system_settings WHERE setting_key = ?`, settingKey)
	return err
}

func RefreshSettingsDuration(scope string) time.Duration {
	setting := GetRefreshSettings(scope)
	delay := time.Duration(setting.Value) * time.Minute
	switch setting.Unit {
	case "s":
		delay = time.Duration(setting.Value) * time.Second
	case "h":
		delay = time.Duration(setting.Value) * time.Hour
	}
	if delay < time.Second {
		return time.Second
	}
	return delay
}

func GetPlatformName() string {
	const fallback = "OpsGuard"
	current := currentStore()
	if current == nil {
		return fallback
	}
	var value string
	if err := current.QueryRow(`SELECT setting_value FROM system_settings WHERE setting_key = 'platform_name'`).Scan(&value); err != nil {
		return fallback
	}
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func SavePlatformName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 40 {
		return "", errors.New("平台名称长度应为 1–40 个字符")
	}
	current := currentStore()
	if current == nil {
		return "", errors.New("system settings store is not initialized")
	}
	_, err := current.Exec(`INSERT INTO system_settings (setting_key, setting_value) VALUES ('platform_name', ?) ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value), updated_at = CURRENT_TIMESTAMP`, value)
	if err != nil {
		return "", err
	}
	return value, nil
}

// GetOperationConfirmations returns the platform-wide confirmation policy for
// destructive database operations. It defaults to enabled for new installs.
func GetOperationConfirmations() bool {
	current := currentStore()
	if current == nil {
		return true
	}
	var value string
	if err := current.QueryRow(`SELECT setting_value FROM system_settings WHERE setting_key = 'require_write_confirmation'`).Scan(&value); err != nil {
		return true
	}
	return value != "false"
}

func SaveOperationConfirmations(required bool) error {
	current := currentStore()
	if current == nil {
		return errors.New("系统设置数据库未初始化")
	}
	_, err := current.Exec(`INSERT INTO system_settings (setting_key, setting_value) VALUES ('require_write_confirmation', ?) ON DUPLICATE KEY UPDATE setting_value=VALUES(setting_value), updated_at=CURRENT_TIMESTAMP`, strconv.FormatBool(required))
	return err
}

// GetMySQLQueryResultLimit returns the maximum number of rows returned by an
// interactive SQL query.
func GetMySQLQueryResultLimit() int {
	current := currentStore()
	if current == nil {
		return 500
	}
	var value string
	if err := current.QueryRow(`SELECT setting_value FROM system_settings WHERE setting_key = 'mysql_query_result_limit'`).Scan(&value); err != nil {
		return 500
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 10000 {
		return 500
	}
	return limit
}

func SaveMySQLQueryResultLimit(limit int) error {
	if limit < 1 || limit > 10000 {
		return errors.New("SQL 查询结果行数必须在 1 到 10000 之间")
	}
	current := currentStore()
	if current == nil {
		return errors.New("系统设置数据库未初始化")
	}
	_, err := current.Exec(`INSERT INTO system_settings (setting_key, setting_value) VALUES ('mysql_query_result_limit', ?) ON DUPLICATE KEY UPDATE setting_value=VALUES(setting_value), updated_at=CURRENT_TIMESTAMP`, strconv.Itoa(limit))
	return err
}

const (
	defaultMySQLQueryResultMaxBytes = 2 * 1024 * 1024
	minMySQLQueryResultMaxBytes     = 1 * 1024 * 1024
	maxMySQLQueryResultMaxBytes     = 100 * 1024 * 1024
)

// GetMySQLQueryResultMaxBytes returns the configured maximum serialized size
// of a SQL result. It is applied together with the row limit.
func GetMySQLQueryResultMaxBytes() int {
	current := currentStore()
	if current == nil {
		return defaultMySQLQueryResultMaxBytes
	}
	var value string
	if err := current.QueryRow(`SELECT setting_value FROM system_settings WHERE setting_key = 'mysql_query_result_max_bytes'`).Scan(&value); err != nil {
		return defaultMySQLQueryResultMaxBytes
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < minMySQLQueryResultMaxBytes || limit > maxMySQLQueryResultMaxBytes {
		return defaultMySQLQueryResultMaxBytes
	}
	return limit
}

func SaveMySQLQueryResultMaxBytes(limit int) error {
	if limit < minMySQLQueryResultMaxBytes || limit > maxMySQLQueryResultMaxBytes {
		return errors.New("SQL 查询结果大小必须在 1 MB 到 100 MB 之间")
	}
	current := currentStore()
	if current == nil {
		return errors.New("系统设置数据库未初始化")
	}
	_, err := current.Exec(`INSERT INTO system_settings (setting_key, setting_value) VALUES ('mysql_query_result_max_bytes', ?) ON DUPLICATE KEY UPDATE setting_value=VALUES(setting_value), updated_at=CURRENT_TIMESTAMP`, strconv.Itoa(limit))
	return err
}

func GetMySQLReportConcurrency() int {
	current := currentStore()
	if current == nil {
		return 2
	}
	var value string
	if err := current.QueryRow(`SELECT setting_value FROM system_settings WHERE setting_key='mysql_report_concurrency'`).Scan(&value); err != nil {
		return 2
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 16 {
		return 2
	}
	return n
}

func SaveMySQLReportConcurrency(value int) error {
	if value < 1 || value > 16 {
		return errors.New("报表任务并发数必须在 1 到 16 之间")
	}
	current := currentStore()
	if current == nil {
		return errors.New("系统设置数据库未初始化")
	}
	_, err := current.Exec(`INSERT INTO system_settings (setting_key, setting_value) VALUES ('mysql_report_concurrency', ?) ON DUPLICATE KEY UPDATE setting_value=VALUES(setting_value), updated_at=CURRENT_TIMESTAMP`, strconv.Itoa(value))
	return err
}

type SSRCustomCommand struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Command  string `json:"command"`
	Category string `json:"category"`
}

func ListSSRCustomCommands() ([]SSRCustomCommand, error) {
	current := currentStore()
	if current == nil {
		return nil, errors.New("平台数据库不可用")
	}
	rows, err := current.Query(`SELECT id, label, command, category FROM ssh_custom_commands ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SSRCustomCommand
	for rows.Next() {
		var item SSRCustomCommand
		if err := rows.Scan(&item.ID, &item.Label, &item.Command, &item.Category); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func SaveSSRCustomCommand(id, label, command, category string) (SSRCustomCommand, error) {
	current := currentStore()
	if current == nil {
		return SSRCustomCommand{}, errors.New("平台数据库不可用")
	}
	if id == "" {
		id = fmt.Sprintf("cmd-%d", time.Now().UnixNano())
		_, err := current.Exec(`INSERT INTO ssh_custom_commands (id, label, command, category) VALUES (?, ?, ?, ?)`, id, label, command, category)
		if err != nil {
			return SSRCustomCommand{}, err
		}
	} else {
		_, err := current.Exec(`UPDATE ssh_custom_commands SET label=?, command=?, category=? WHERE id=?`, label, command, category, id)
		if err != nil {
			return SSRCustomCommand{}, err
		}
	}
	return SSRCustomCommand{ID: id, Label: label, Command: command, Category: category}, nil
}

func DeleteSSRCustomCommand(id string) error {
	current := currentStore()
	if current == nil {
		return errors.New("平台数据库不可用")
	}
	_, err := current.Exec(`DELETE FROM ssh_custom_commands WHERE id=?`, id)
	return err
}

type SSHCommandHistory struct {
	ID         int64  `json:"id"`
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exitCode"`
	CreatedAt  string `json:"createdAt"`
}

func RecordSSHCommandHistory(sourceID, sourceName, command string, exitCode int) {
	current := currentStore()
	if current == nil {
		return
	}
	current.Exec(`INSERT INTO ssh_command_history (source_id, source_name, command, exit_code) VALUES (?, ?, ?, ?)`, sourceID, sourceName, command, exitCode)
}

func ListSSHCommandHistory(sourceID string, limit int) ([]SSHCommandHistory, error) {
	current := currentStore()
	if current == nil {
		return nil, errors.New("平台数据库不可用")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if sourceID != "" {
		rows, err = current.Query(`SELECT id, source_id, source_name, command, exit_code, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') FROM ssh_command_history WHERE source_id = ? ORDER BY id DESC LIMIT ?`, sourceID, limit)
	} else {
		rows, err = current.Query(`SELECT id, source_id, source_name, command, exit_code, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') FROM ssh_command_history ORDER BY id DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SSHCommandHistory
	for rows.Next() {
		var h SSHCommandHistory
		if err := rows.Scan(&h.ID, &h.SourceID, &h.SourceName, &h.Command, &h.ExitCode, &h.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, h)
	}
	return result, nil
}
