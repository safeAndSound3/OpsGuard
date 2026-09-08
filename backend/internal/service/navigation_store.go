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

func ListDashboardItems() []model.DashboardItem {
	current := currentStore()
	if current == nil {
		return []model.DashboardItem{}
	}
	rows, err := current.Query(`SELECT dashboard.id, dashboard.name, dashboard.source_id, source.name, source.type, dashboard.created_at
		FROM dashboard_items dashboard
		INNER JOIN data_sources source ON source.id = dashboard.source_id
		WHERE source.type IN ('MySQL', 'SSH', 'Redis', 'ClickHouse', 'Kafka')
		ORDER BY CASE source.type WHEN 'SSH' THEN 0 WHEN 'MySQL' THEN 1 WHEN 'Redis' THEN 2 WHEN 'ClickHouse' THEN 3 WHEN 'Kafka' THEN 4 ELSE 9 END, source.name ASC, dashboard.name ASC, dashboard.id ASC`)
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
	if !strings.EqualFold(source.Type, "mysql") && !strings.EqualFold(source.Type, "ssh") && !strings.EqualFold(source.Type, "redis") && !strings.EqualFold(source.Type, "clickhouse") && !strings.EqualFold(source.Type, "kafka") {
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
	default:
		return "refresh_interval", model.RefreshSettings{Value: 15, Unit: "s"}
	}
}

func GetRefreshSettings(scopes ...string) model.RefreshSettings {
	scope := ""
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	settingKey, settings := refreshSettingDefinition(scope)
	current := currentStore()
	if current == nil {
		return settings
	}
	var raw string
	if err := current.QueryRow(`SELECT setting_value FROM system_settings WHERE setting_key = ?`, settingKey).Scan(&raw); err != nil {
		return settings
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return settings
	}
	value, err := strconv.Atoi(parts[0])
	unit := parts[1]
	if err != nil || value < 1 || (unit != "s" && unit != "m" && unit != "h") {
		return settings
	}
	settings.Value = value
	settings.Unit = unit
	settings.Configured = true
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
