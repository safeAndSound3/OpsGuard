package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"monitor-platform/internal/model"
)

var prometheusAlertSynchronizerOnce sync.Once

func startPrometheusAlertSynchronizer() {
	prometheusAlertSynchronizerOnce.Do(func() {
		go func() {
			syncPrometheusAlertNotifications()
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				syncPrometheusAlertNotifications()
			}
		}()
	})
}

// syncPrometheusAlertNotifications mirrors firing alerts from every enabled
// Prometheus source into the platform notification center.
func syncPrometheusAlertNotifications() {
	current := currentStore()
	if current == nil {
		return
	}
	for _, source := range ListDataSources() {
		if !source.Enabled || !strings.EqualFold(source.Type, "prometheus") {
			continue
		}
		alerts, err := ListPrometheusDataSourceAlerts(source.ID)
		if err != nil {
			// Do not turn alerts into recoveries when Prometheus cannot be reached.
			continue
		}
		seen := make([]string, 0, len(alerts))
		for _, alert := range alerts {
			if !strings.EqualFold(alert.State, "firing") {
				continue
			}
			id := prometheusAlertNotificationID(source.ID, alert)
			seen = append(seen, upsertPrometheusAlertNotification(current, id, source, alert))
		}
		resolveMissingPrometheusAlertNotifications(current, source.ID, seen)
	}
}

func prometheusAlertNotificationID(sourceID string, alert model.PrometheusAlert) string {
	labels := make([]string, 0, len(alert.Labels))
	for key, value := range alert.Labels {
		labels = append(labels, key+"="+value)
	}
	sort.Strings(labels)
	sum := sha256.Sum256([]byte(alert.Name + "\x00" + strings.Join(labels, "\x00")))
	return fmt.Sprintf("prom-%s-%x", sourceID, sum[:8])
}

func upsertPrometheusAlertNotification(appDB *sql.DB, id string, source model.DataSource, alert model.PrometheusAlert) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	message := prometheusAlertMessage(alert)
	labels := prometheusAlertLabels(alert.Labels)
	severity := firstNonEmpty(alert.Severity, "warning")
	now := time.Now()
	var activeID string
	if err := appDB.QueryRowContext(ctx, `SELECT id FROM alert_notifications
		WHERE source = ? AND database_name = 'prometheus' AND rule_name = ? AND field_name = ? AND status = 'active'
		ORDER BY first_seen_at DESC LIMIT 1`, source.ID, alert.Name, labels).Scan(&activeID); err == nil {
		_, _ = appDB.ExecContext(ctx, `UPDATE alert_notifications
			SET table_name = ?, severity = ?, message = ?, unread = IF(muted = 1, 0, 1), last_seen_at = ?
			WHERE id = ?`, source.Name, severity, message, now, activeID)
		return activeID
	}
	id = fmt.Sprintf("%s-alert-%d", id, time.Now().UnixNano())
	_, _ = appDB.ExecContext(ctx, `INSERT INTO alert_notifications
		(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, 'prometheus', ?, ?, ?, 'active', ?, 1, ?, ?)`,
		id, "prometheus:"+source.ID, alert.Name, source.ID, source.Name, labels, severity, message, now, now)
	return id
}

func prometheusAlertMessage(alert model.PrometheusAlert) string {
	summary := strings.TrimSpace(alert.Summary)
	description := strings.TrimSpace(alert.Description)
	if summary != "" && description != "" && summary != description {
		return summary + " · " + description
	}
	return firstNonEmpty(summary, description, "Prometheus 告警触发")
}

func resolveMissingPrometheusAlertNotifications(appDB *sql.DB, sourceID string, seen []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	query := `SELECT id, rule_id, rule_name, table_name, field_name, severity, message, first_seen_at
		FROM alert_notifications WHERE source = ? AND database_name = 'prometheus' AND status = 'active'`
	args := []any{sourceID}
	if len(seen) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(seen)), ",")
		query += " AND id NOT IN (" + placeholders + ")"
		for _, id := range seen {
			args = append(args, id)
		}
	}
	rows, err := appDB.QueryContext(ctx, query, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var id, ruleID, ruleName, tableName, fieldName, severity, message string
		var firstSeenAt time.Time
		if err := rows.Scan(&id, &ruleID, &ruleName, &tableName, &fieldName, &severity, &message, &firstSeenAt); err != nil {
			continue
		}
		_, _ = appDB.ExecContext(ctx, `UPDATE alert_notifications SET status = 'alert', unread = IF(muted = 1, 0, 1) WHERE id = ?`, id)
		_, _ = appDB.ExecContext(ctx, `INSERT INTO alert_notifications
			(id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, first_seen_at, last_seen_at, resolved_at)
			VALUES (?, ?, ?, ?, 'prometheus', ?, ?, ?, 'resolved', ?, 1, ?, ?, ?)`,
			fmt.Sprintf("%s-recovered-%d", id, time.Now().UnixNano()), ruleID, ruleName, sourceID, tableName, fieldName, severity, "已恢复："+message, firstSeenAt, now, now)
	}
}

func prometheusAlertLabels(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for key, value := range labels {
		if key == "alertname" {
			continue
		}
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
