package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"monitor-platform/internal/model"
)

type ReportRun struct {
	ID         string               `json:"id"`
	Status     string               `json:"status"`
	Message    string               `json:"message"`
	StartedAt  string               `json:"startedAt"`
	FinishedAt string               `json:"finishedAt,omitempty"`
	DurationMs int64                `json:"durationMs,omitempty"`
	Result     MySQLStatementResult `json:"result"`
}

type ReportRunSummary struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
	RowCount   int    `json:"rowCount"`
	Truncated  bool   `json:"truncated"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

var reportWorkers = make(chan struct{}, 16)
var reportSchedulerOnce sync.Once

func startReportScheduler() error {
	db := currentStore()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS mysql_report_runs (
 id varchar(64) PRIMARY KEY, task_id varchar(64) NOT NULL, status varchar(24) NOT NULL,
 message text NOT NULL, started_at datetime NOT NULL, finished_at datetime NULL,
 result_json longtext NOT NULL, INDEX idx_report_runs_task(task_id,started_at))`)
	if err != nil {
		return err
	}
	reportSchedulerOnce.Do(func() {
		_, _ = db.Exec(`UPDATE mysql_report_runs SET status='失败', message='服务重启，执行已中断', finished_at=NOW() WHERE status='执行中'`)
		_, _ = db.Exec(`UPDATE mysql_report_tasks SET status='失败', last_message='服务重启，执行已中断' WHERE status='执行中'`)
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for tick := range ticker.C {
				now := tick.Truncate(time.Second)
				for _, task := range ListMySQLReportTasks("") {
					if !task.Enabled || task.Status == "执行中" || task.Schedule == "手动" {
						continue
					}
					cron, err := parseReportCron(task.Schedule)
					if err != nil {
						_, _ = db.Exec(`UPDATE mysql_report_tasks SET status='配置错误',last_message=? WHERE id=?`, err.Error(), task.ID)
						continue
					}
					if due, ok := reportDueTime(task, cron, now); ok {
						if err := launchReport(task, due, true); err != nil {
							log.Printf("报表调度 %s: %v", task.ID, err)
						}
					}
				}
			}
		}()
	})
	return nil
}

func reportDueTime(task model.MySQLReportTask, cron reportCron, now time.Time) (time.Time, bool) {
	current := now.Truncate(time.Second)
	if cron.matches(current) {
		return current, true
	}
	// Perform catch-up checks on minute boundaries to keep normal polling cheap.
	if current.Second() != 0 {
		return time.Time{}, false
	}
	lower := current.Add(-24 * time.Hour)
	if task.LastRunAt != nil {
		if last, err := time.Parse(time.RFC3339, *task.LastRunAt); err == nil && last.After(lower) {
			lower = last.Add(time.Second)
		}
	}
	for candidate := current.Add(-time.Second); !candidate.Before(lower); candidate = candidate.Add(-time.Second) {
		if cron.matches(candidate) {
			return candidate, true
		}
	}
	return time.Time{}, false
}

func RunReportNow(id string) error {
	for _, task := range ListMySQLReportTasks("") {
		if task.ID == id {
			return launchReport(task, time.Now().Truncate(time.Second), false)
		}
	}
	return errors.New("报表任务不存在")
}

func launchReport(task model.MySQLReportTask, now time.Time, scheduled bool) error {
	if len(reportWorkers) >= GetMySQLReportConcurrency() {
		return errors.New("报表执行槽位已满，请稍后重试")
	}
	reportWorkers <- struct{}{}
	release := true
	defer func() {
		if release {
			<-reportWorkers
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db := currentStore()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `UPDATE mysql_report_tasks SET status='执行中',last_run_at=?,last_message='正在执行' WHERE id=? AND status<>'执行中' AND schedule=? AND sql_text=?`
	args := []any{now, task.ID, task.Schedule, task.SQL}
	if scheduled {
		query += ` AND enabled=1 AND (last_run_at IS NULL OR last_run_at<?)`
		args = append(args, now)
	}
	updated, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, _ := updated.RowsAffected()
	if n == 0 {
		return nil
	}
	id := fmt.Sprintf("report-run-%d", time.Now().UnixNano())
	_, err = tx.ExecContext(ctx, `INSERT INTO mysql_report_runs(id,task_id,status,message,started_at,result_json) VALUES (?,?,'执行中','正在执行',?,'{}')`, id, task.ID, now)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	release = false
	go func() {
		defer func() { <-reportWorkers }()
		result, executionErr := executeReportSQL(task)
		status, message := "成功", fmt.Sprintf("查询完成，共 %d 行", len(result.Rows))
		if result.Truncated {
			message += "（达到结果上限，已截断）"
		}
		if executionErr != nil {
			status = "失败"
			message = executionErr.Error()
		}
		raw, _ := json.Marshal(result)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("保存报表结果失败: %v", err)
			return
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `UPDATE mysql_report_runs SET status=?,message=?,finished_at=NOW(),result_json=? WHERE id=?`, status, message, string(raw), id)
		if err == nil {
			brief := []rune(message)
			if len(brief) > 255 {
				brief = brief[:255]
			}
			_, err = tx.ExecContext(ctx, `UPDATE mysql_report_tasks SET status=?,last_message=? WHERE id=? AND last_run_at=?`, status, string(brief), task.ID, now)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			log.Printf("保存报表结果失败: %v", err)
		}
		if source, err := GetDataSourceByID(task.SourceID); err == nil && strings.EqualFold(source.Type, "mysql") {
			recordMySQLSQLHistory(task.SourceID, "", redactSQLForHistory(task.SQL), "报表调度", result, executionErr, time.Since(now))
		}
		if task.AlertEnabled && executionErr == nil {
			checkReportAlert(task, len(result.Rows), id, db)
		}
		_, _ = db.Exec(`DELETE FROM mysql_report_runs WHERE finished_at < DATE_SUB(NOW(), INTERVAL 180 DAY)`)
	}()
	return nil
}

func checkReportAlert(task model.MySQLReportTask, rowCount int, currentRunID string, db *sql.DB) {
	if task.AlertOperator == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var prevRowCount int
	if task.AlertCompare == "last" {
		var prevMessage string
		_ = db.QueryRowContext(ctx, `SELECT message FROM mysql_report_runs WHERE task_id=? AND status='成功' AND id<>? ORDER BY started_at DESC LIMIT 1`, task.ID, currentRunID).Scan(&prevMessage)
		if prevMessage != "" {
			if n, err := fmt.Sscanf(prevMessage, "查询完成，共 %d 行", &prevRowCount); err != nil || n != 1 {
				prevRowCount = 0
			}
		}
	}

	var actual float64
	if task.AlertCompare == "last" {
		if prevRowCount == 0 {
			return
		}
		diff := float64(rowCount-prevRowCount) / float64(prevRowCount) * 100
		actual = diff
	} else {
		actual = float64(rowCount)
	}

	triggered := false
	switch task.AlertOperator {
	case ">":
		triggered = actual > task.AlertThreshold
	case ">=":
		triggered = actual >= task.AlertThreshold
	case "<":
		triggered = actual < task.AlertThreshold
	case "<=":
		triggered = actual <= task.AlertThreshold
	case "=":
		triggered = actual == task.AlertThreshold
	case "empty":
		triggered = rowCount == 0
	}
	if !triggered {
		return
	}
	unit := task.AlertUnit
	unitLabel := "行"
	if unit == "pct" {
		unitLabel = "%"
	}
	var msg string
	if task.AlertCompare == "last" {
		msg = fmt.Sprintf("报表任务「%s」触发告警：相比上次变化率 %.1f%s，条件 %s %g%s（本次 %d 行，上次 %d 行）", task.Name, actual, unitLabel, task.AlertOperator, task.AlertThreshold, unitLabel, rowCount, prevRowCount)
	} else if task.AlertOperator == "empty" {
		msg = fmt.Sprintf("报表任务「%s」触发告警：本次查询结果为空", task.Name)
	} else {
		msg = fmt.Sprintf("报表任务「%s」触发告警：本次 %d 行，条件 %s %g%s", task.Name, rowCount, task.AlertOperator, task.AlertThreshold, unitLabel)
	}
	alertID := fmt.Sprintf("report-alert-%d", time.Now().UnixNano())
	_, _ = db.ExecContext(ctx, `INSERT INTO alert_notifications (id, rule_id, rule_name, source, database_name, table_name, field_name, severity, status, message, unread, muted, first_seen_at, last_seen_at) VALUES (?,?,?,?,?,?,?,?,?, ?,1,0,NOW(),NOW())`,
		alertID, task.ID, task.Name, "报表任务", "", "", "", "warning", "alert", msg)
}

func executeReportSQL(task model.MySQLReportTask) (MySQLStatementResult, error) {
	result := MySQLStatementResult{Columns: []string{}, Rows: [][]any{}}
	if !isReadOnlyReportSQL(task.SQL) {
		return result, errors.New("报表只允许单条只读查询")
	}
	query, err := renderReportSQL(task.SQL, task.TimeRange, time.Now())
	if err != nil {
		return result, err
	}
	source, err := GetDataSourceByID(task.SourceID)
	if err != nil {
		return result, err
	}
	if !source.Enabled || !isReportDataSourceType(source.Type) {
		return result, errors.New("报表数据源不可用")
	}
	if strings.EqualFold(source.Type, "hive") {
		return executeHiveReportSQL(task.SourceID, query)
	}
	var db *sql.DB
	if strings.EqualFold(source.Type, "mysql") {
		db, err = openMySQLDataSource(source)
	} else if strings.EqualFold(source.Type, "clickhouse") {
		db, err = openClickHouseDataSource(source)
	} else {
		db, err = openPostgreSQLDataSource(source, "")
	}
	if err != nil {
		return result, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var rows *sql.Rows
	if strings.EqualFold(source.Type, "clickhouse") {
		rows, err = db.QueryContext(ctx, query)
	} else {
		tx, txErr := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if txErr != nil {
			return result, txErr
		}
		defer tx.Rollback()
		rows, err = tx.QueryContext(ctx, query)
	}
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Columns, err = rows.Columns()
	if err != nil {
		return result, err
	}
	maxRows, maxBytes := GetMySQLQueryResultLimit(), GetMySQLQueryResultMaxBytes()
	raw, _ := json.Marshal(result.Columns)
	used := len(raw)
	for rows.Next() {
		if len(result.Rows) >= maxRows {
			result.Truncated = true
			break
		}
		values := make([]any, len(result.Columns))
		refs := make([]any, len(values))
		for i := range values {
			refs[i] = &values[i]
		}
		if err = rows.Scan(refs...); err != nil {
			return result, err
		}
		for i := range values {
			values[i] = mysqlJSONValue(values[i])
		}
		raw, err = json.Marshal(values)
		if err != nil {
			return result, err
		}
		if used+len(raw) > maxBytes {
			result.Truncated = true
			break
		}
		used += len(raw)
		result.Rows = append(result.Rows, values)
	}
	return result, rows.Err()
}

func executeHiveReportSQL(sourceID, query string) (MySQLStatementResult, error) {
	result := MySQLStatementResult{Columns: []string{}, Rows: [][]any{}}
	conn, err := hiveConnection(sourceID)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cursor := conn.Cursor()
	cursor.Execute(ctx, query, false)
	if cursor.Err != nil {
		return result, formatHiveQueryError(cursor.Err)
	}
	for _, column := range cursor.Description() {
		if len(column) > 0 {
			result.Columns = append(result.Columns, column[0])
		}
	}
	if cursor.Err != nil {
		return result, formatHiveQueryError(cursor.Err)
	}
	maxRows, maxBytes := GetMySQLQueryResultLimit(), GetMySQLQueryResultMaxBytes()
	raw, _ := json.Marshal(result.Columns)
	used := len(raw)
	for cursor.HasMore(ctx) {
		if cursor.Err != nil {
			return result, formatHiveQueryError(cursor.Err)
		}
		if len(result.Rows) >= maxRows {
			result.Truncated = true
			break
		}
		row := cursor.RowMap(ctx)
		if cursor.Err != nil {
			return result, formatHiveQueryError(cursor.Err)
		}
		values := make([]any, len(result.Columns))
		for i, name := range result.Columns {
			values[i] = mysqlJSONValue(row[name])
		}
		raw, err = json.Marshal(values)
		if err != nil {
			return result, err
		}
		if used+len(raw) > maxBytes {
			result.Truncated = true
			break
		}
		used += len(raw)
		result.Rows = append(result.Rows, values)
	}
	if cursor.Err != nil {
		return result, formatHiveQueryError(cursor.Err)
	}
	return result, nil
}

// renderReportSQL replaces the two supported report variables with local-time
// values. A task without a time range must use explicit dates or MySQL functions.
func renderReportSQL(query, timeRange string, now time.Time) (string, error) {
	if !strings.Contains(query, "${") {
		return query, nil
	}
	start, end, err := reportTimeWindow(strings.TrimSpace(timeRange), now)
	if err != nil {
		return "", err
	}
	return strings.NewReplacer("${start}", start.Format("2006-01-02 15:04:05"), "${end}", end.Format("2006-01-02 15:04:05")).Replace(query), nil
}

func reportTimeWindow(mode string, now time.Time) (time.Time, time.Time, error) {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	day := 24 * time.Hour
	switch mode {
	case "昨天":
		return today.Add(-day), today, nil
	case "今天":
		return today, today.Add(day), nil
	case "上周":
		mondayOffset := (int(today.Weekday()) + 6) % 7
		thisMonday := today.Add(-time.Duration(mondayOffset) * day)
		return thisMonday.Add(-7 * day), thisMonday, nil
	case "本周":
		mondayOffset := (int(today.Weekday()) + 6) % 7
		thisMonday := today.Add(-time.Duration(mondayOffset) * day)
		return thisMonday, thisMonday.Add(7 * day), nil
	case "上月":
		thisMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc)
		return thisMonth.AddDate(0, -1, 0), thisMonth, nil
	case "本月":
		thisMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc)
		return thisMonth, thisMonth.AddDate(0, 1, 0), nil
	case "近7天":
		return now.AddDate(0, 0, -7), now, nil
	case "近30天":
		return now.AddDate(0, 0, -30), now, nil
	case "上周六到本周五":
		wd := int(today.Weekday())
		var thisFriday time.Time
		if wd <= 5 {
			thisFriday = today.AddDate(0, 0, 5-wd)
		} else {
			thisFriday = today.AddDate(0, 0, -(wd - 5))
		}
		saturdayStart := thisFriday.AddDate(0, 0, -6)
		return saturdayStart, thisFriday.Add(day), nil
	case "":
		return time.Time{}, time.Time{}, errors.New("SQL 使用时间变量时必须选择统计时间")
	}
	parts := strings.Split(mode, "|")
	if len(parts) == 3 && parts[0] == "自定义" {
		start, startErr := time.ParseInLocation("2006-01-02 15:04:05", parts[1], loc)
		end, endErr := time.ParseInLocation("2006-01-02 15:04:05", parts[2], loc)
		if startErr == nil && endErr == nil && start.Before(end) {
			return start, end, nil
		}
	}
	return time.Time{}, time.Time{}, errors.New("统计时间配置无效")
}

func LatestReportRun(taskID string) (ReportRun, error) {
	var result ReportRun
	var started time.Time
	var finished sqlNullTime
	var raw string
	err := currentStore().QueryRow(`SELECT run.id,run.status,run.message,run.started_at,run.finished_at,run.result_json
		FROM mysql_report_runs run JOIN mysql_report_tasks task ON task.id=run.task_id
		WHERE task.id=? AND task.last_run_at=run.started_at
		ORDER BY run.started_at DESC,run.id DESC LIMIT 1`, taskID).Scan(&result.ID, &result.Status, &result.Message, &started, &finished, &raw)
	if err != nil {
		return result, errors.New("暂无当前配置的执行结果")
	}
	result.StartedAt = started.Format(time.RFC3339)
	if finished.Valid {
		result.FinishedAt = finished.Time.Format(time.RFC3339)
		result.DurationMs = finished.Time.Sub(started).Milliseconds()
	}
	err = json.Unmarshal([]byte(raw), &result.Result)
	return result, err
}

func ListReportRuns(taskID string, page, pageSize int) (map[string]any, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	db := currentStore()
	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mysql_report_runs WHERE task_id=?`, taskID).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id,status,message,started_at,finished_at,result_json FROM mysql_report_runs WHERE task_id=? ORDER BY started_at DESC,id DESC LIMIT ? OFFSET ?`, taskID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ReportRunSummary, 0)
	for rows.Next() {
		var item ReportRunSummary
		var started time.Time
		var finished sqlNullTime
		var raw string
		if err := rows.Scan(&item.ID, &item.Status, &item.Message, &started, &finished, &raw); err != nil {
			return nil, err
		}
		item.StartedAt = started.Format(time.RFC3339)
		if finished.Valid {
			item.FinishedAt = finished.Time.Format(time.RFC3339)
			item.DurationMs = finished.Time.Sub(started).Milliseconds()
		}
		var result MySQLStatementResult
		if json.Unmarshal([]byte(raw), &result) == nil {
			item.RowCount, item.Truncated = len(result.Rows), result.Truncated
		}
		items = append(items, item)
	}
	return map[string]any{"items": items, "page": page, "pageSize": pageSize, "total": total, "totalPages": maxInt(1, (total+pageSize-1)/pageSize)}, nil
}

type ReportTrendPoint struct {
	Time     string `json:"time"`
	RowCount int    `json:"rowCount"`
	Status   string `json:"status"`
}

type ReportTrendTask struct {
	TaskID   string             `json:"taskId"`
	TaskName string             `json:"taskName"`
	Points   []ReportTrendPoint `json:"points"`
}

func ReportTrend(days int, start, end string) ([]ReportTrendTask, error) {
	db := currentStore()
	var rows *sql.Rows
	var err error
	if start != "" && end != "" {
		rows, err = db.Query(`SELECT task.id, task.name, run.started_at, run.status, run.result_json
			FROM mysql_report_runs run JOIN mysql_report_tasks task ON task.id=run.task_id
			WHERE run.started_at >= ? AND run.started_at <= ?
			ORDER BY task.id, run.started_at ASC`, start, end)
	} else {
		if days < 1 || days > 365 {
			days = 7
		}
		rows, err = db.Query(`SELECT task.id, task.name, run.started_at, run.status, run.result_json
			FROM mysql_report_runs run JOIN mysql_report_tasks task ON task.id=run.task_id
			WHERE run.started_at >= DATE_SUB(NOW(), INTERVAL ? DAY)
			ORDER BY task.id, run.started_at ASC`, days)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	taskMap := map[string]*ReportTrendTask{}
	var order []string
	for rows.Next() {
		var taskID, taskName, status, raw string
		var started time.Time
		if err := rows.Scan(&taskID, &taskName, &started, &status, &raw); err != nil {
			return nil, err
		}
		t, ok := taskMap[taskID]
		if !ok {
			t = &ReportTrendTask{TaskID: taskID, TaskName: taskName, Points: []ReportTrendPoint{}}
			taskMap[taskID] = t
			order = append(order, taskID)
		}
		point := ReportTrendPoint{Time: started.Format(time.RFC3339), Status: status}
		var result MySQLStatementResult
		if json.Unmarshal([]byte(raw), &result) == nil {
			point.RowCount = len(result.Rows)
		}
		t.Points = append(t.Points, point)
	}
	out := make([]ReportTrendTask, 0, len(order))
	for _, id := range order {
		out = append(out, *taskMap[id])
	}
	return out, nil
}

func ReportRunByID(taskID, runID string) (ReportRun, error) {
	var result ReportRun
	var started time.Time
	var raw string
	var finished sqlNullTime
	err := currentStore().QueryRow(`SELECT id,status,message,started_at,finished_at,result_json FROM mysql_report_runs WHERE task_id=? AND id=?`, taskID, runID).Scan(&result.ID, &result.Status, &result.Message, &started, &finished, &raw)
	if err != nil {
		return result, errors.New("执行记录不存在")
	}
	result.StartedAt = started.Format(time.RFC3339)
	if finished.Valid {
		result.FinishedAt = finished.Time.Format(time.RFC3339)
		result.DurationMs = finished.Time.Sub(started).Milliseconds()
	}
	err = json.Unmarshal([]byte(raw), &result.Result)
	return result, err
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ReportResultCSV exports the most recently persisted result without rerunning SQL.
func ReportResultCSV(taskID string) ([]byte, string, error) {
	run, err := LatestReportRun(taskID)
	if err != nil {
		return nil, "", err
	}
	return reportRunCSV(taskID, run)
}

func ReportRunCSV(taskID, runID string) ([]byte, string, error) {
	run, err := ReportRunByID(taskID, runID)
	if err != nil {
		return nil, "", err
	}
	return reportRunCSV(taskID, run)
}

func reportRunCSV(taskID string, run ReportRun) ([]byte, string, error) {
	var name string
	var err error
	if err = currentStore().QueryRow(`SELECT name FROM mysql_report_tasks WHERE id=?`, taskID).Scan(&name); err != nil {
		return nil, "", errors.New("报表任务不存在")
	}
	name = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_").Replace(strings.TrimSpace(name))
	if name == "" {
		name = "报表结果"
	}
	var buffer bytes.Buffer
	buffer.WriteString("\xEF\xBB\xBF")
	writer := csv.NewWriter(&buffer)
	if err = writer.Write(safeCSVRecord(run.Result.Columns)); err != nil {
		return nil, "", err
	}
	for _, row := range run.Result.Rows {
		values := make([]string, len(row))
		for i, value := range row {
			if value != nil {
				values[i] = safeCSVCell(fmt.Sprint(value))
			}
		}
		if err = writer.Write(values); err != nil {
			return nil, "", err
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), name + "-结果.csv", nil
}
