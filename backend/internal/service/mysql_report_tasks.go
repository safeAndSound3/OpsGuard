package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

func ListMySQLReportTasks(sourceID string) []model.MySQLReportTask {
	current := currentStore()
	if current == nil {
		return []model.MySQLReportTask{}
	}
	query := `SELECT task.id, task.name, task.source_id, COALESCE(source.name, '已删除数据源'), task.sql_text, task.time_range, task.schedule, task.enabled, task.status, task.last_run_at, task.last_message, COALESCE(task.alert_enabled, 0), COALESCE(task.alert_compare, ''), COALESCE(task.alert_operator, ''), COALESCE(task.alert_threshold, 0), COALESCE(task.alert_unit, ''), task.created_at, task.updated_at
		FROM mysql_report_tasks task LEFT JOIN data_sources source ON source.id = task.source_id`
	args := []any{}
	if strings.TrimSpace(sourceID) != "" {
		query += ` WHERE task.source_id = ?`
		args = append(args, strings.TrimSpace(sourceID))
	}
	query += ` ORDER BY task.updated_at DESC, task.id DESC`
	rows, err := current.Query(query, args...)
	if err != nil {
		return []model.MySQLReportTask{}
	}
	defer rows.Close()
	items := []model.MySQLReportTask{}
	for rows.Next() {
		var item model.MySQLReportTask
		var lastRun sqlNullTime
		if rows.Scan(&item.ID, &item.Name, &item.SourceID, &item.SourceName, &item.SQL, &item.TimeRange, &item.Schedule, &item.Enabled, &item.Status, &lastRun, &item.LastMessage, &item.AlertEnabled, &item.AlertCompare, &item.AlertOperator, &item.AlertThreshold, &item.AlertUnit, &item.CreatedAt, &item.UpdatedAt) == nil {
			if lastRun.Valid {
				value := lastRun.Time.Format(time.RFC3339)
				item.LastRunAt = &value
			}
			items = append(items, item)
		}
	}
	return items
}

// sqlNullTime keeps the service independent from a specific SQL driver's nullable type.
type sqlNullTime struct {
	time.Time
	Valid bool
}

func (value *sqlNullTime) Scan(input any) error {
	if input == nil {
		value.Valid = false
		return nil
	}
	switch parsed := input.(type) {
	case time.Time:
		value.Time, value.Valid = parsed, true
		return nil
	case []byte:
		parsedTime, err := time.Parse("2006-01-02 15:04:05", string(parsed))
		if err != nil {
			return err
		}
		value.Time, value.Valid = parsedTime, true
		return nil
	case string:
		parsedTime, err := time.Parse("2006-01-02 15:04:05", parsed)
		if err != nil {
			return err
		}
		value.Time, value.Valid = parsedTime, true
		return nil
	default:
		return fmt.Errorf("unsupported nullable time")
	}
}

func SaveMySQLReportTask(task model.MySQLReportTask) (model.MySQLReportTask, error) {
	task.ID = strings.TrimSpace(task.ID)
	task.Name = strings.TrimSpace(task.Name)
	task.SourceID = strings.TrimSpace(task.SourceID)
	task.SQL = strings.TrimSpace(task.SQL)
	task.TimeRange = strings.TrimSpace(task.TimeRange)
	task.Schedule = strings.TrimSpace(task.Schedule)
	if task.Name == "" || len([]rune(task.Name)) > 120 {
		return model.MySQLReportTask{}, errors.New("报表任务名称不能为空且不能超过 120 个字符")
	}
	if task.SQL == "" || len(task.SQL) > 1<<20 {
		return model.MySQLReportTask{}, errors.New("报表 SQL 不能为空且不能超过 1 MiB")
	}
	if !isReadOnlyReportSQL(task.SQL) {
		return model.MySQLReportTask{}, errors.New("报表任务只允许只读查询 SQL")
	}
	source, err := GetDataSourceByID(task.SourceID)
	if err != nil || !isReportDataSourceType(source.Type) {
		return model.MySQLReportTask{}, errors.New("报表数据源不存在或类型不支持（仅支持 MySQL、PostgreSQL、Hive）")
	}
	if task.TimeRange != "" {
		if _, _, err := reportTimeWindow(task.TimeRange, time.Now()); err != nil {
			return model.MySQLReportTask{}, err
		}
	}
	if task.Schedule == "" {
		task.Schedule = "手动"
	}
	if task.Schedule != "手动" {
		if _, err := parseReportCron(task.Schedule); err != nil {
			return model.MySQLReportTask{}, err
		}
	}
	if task.ID == "" {
		task.ID = fmt.Sprintf("report-%d", time.Now().UnixNano())
	}
	if task.Status == "" {
		task.Status = "待执行"
	}
	current := currentStore()
	if current == nil {
		return model.MySQLReportTask{}, errors.New("平台数据库不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = current.ExecContext(ctx, `INSERT INTO mysql_report_tasks (id, name, source_id, sql_text, time_range, schedule, enabled, status, last_message, alert_enabled, alert_compare, alert_operator, alert_threshold, alert_unit) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		status=IF(source_id<>VALUES(source_id) OR sql_text<>VALUES(sql_text) OR time_range<>VALUES(time_range), '待执行', status),
		last_run_at=IF(source_id<>VALUES(source_id) OR sql_text<>VALUES(sql_text) OR time_range<>VALUES(time_range), NULL, last_run_at),
		last_message=IF(source_id<>VALUES(source_id) OR sql_text<>VALUES(sql_text) OR time_range<>VALUES(time_range), '配置已修改，请重新执行', last_message),
		name=VALUES(name), source_id=VALUES(source_id), sql_text=VALUES(sql_text), time_range=VALUES(time_range), schedule=VALUES(schedule), enabled=VALUES(enabled), alert_enabled=VALUES(alert_enabled), alert_compare=VALUES(alert_compare), alert_operator=VALUES(alert_operator), alert_threshold=VALUES(alert_threshold), alert_unit=VALUES(alert_unit), updated_at=CURRENT_TIMESTAMP`, task.ID, task.Name, task.SourceID, task.SQL, task.TimeRange, task.Schedule, boolInt(task.Enabled), task.Status, task.LastMessage, boolInt(task.AlertEnabled), task.AlertCompare, task.AlertOperator, task.AlertThreshold, task.AlertUnit)
	if err != nil {
		return model.MySQLReportTask{}, err
	}
	for _, item := range ListMySQLReportTasks(task.SourceID) {
		if item.ID == task.ID {
			return item, nil
		}
	}
	return task, nil
}

func isReportDataSourceType(sourceType string) bool {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "mysql", "postgresql", "postgres", "hive", "clickhouse":
		return true
	default:
		return false
	}
}

func DeleteMySQLReportTask(id string) error {
	current := currentStore()
	if current == nil {
		return errors.New("平台数据库不可用")
	}
	result, err := current.Exec(`DELETE FROM mysql_report_tasks WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return errors.New("报表任务不存在")
	}
	return nil
}

func SetMySQLReportTaskEnabled(id string, enabled bool) (model.MySQLReportTask, error) {
	current := currentStore()
	if current == nil {
		return model.MySQLReportTask{}, errors.New("平台数据库不可用")
	}
	if _, err := current.Exec(`UPDATE mysql_report_tasks SET enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, boolInt(enabled), strings.TrimSpace(id)); err != nil {
		return model.MySQLReportTask{}, err
	}
	for _, item := range ListMySQLReportTasks("") {
		if item.ID == id {
			return item, nil
		}
	}
	return model.MySQLReportTask{}, errors.New("报表任务不存在")
}

var reportUnsafeSQL = regexp.MustCompile(`(?i)\b(insert|update|delete|replace|create|drop|alter|truncate|call|set|grant|revoke|outfile|dumpfile|load|lock|sleep|benchmark|get_lock|release_lock)\b`)

func isReadOnlyReportSQL(statement string) bool {
	parts, err := splitMySQLStatements(statement)
	if err != nil || len(parts) != 1 || strings.Contains(statement, "/*!") || reportUnsafeSQL.MatchString(statement) {
		return false
	}
	first := mysqlStatementKeyword(parts[0])
	return first == "SELECT" || first == "SHOW" || first == "DESCRIBE" || first == "DESC" || first == "EXPLAIN" || first == "WITH"
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
