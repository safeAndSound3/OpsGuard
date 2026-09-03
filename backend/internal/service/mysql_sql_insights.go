package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"monitor-platform/internal/model"
)

// ListMySQLDashboardSQL returns actual slow-log SQL first. If slow-log output
// is unavailable or empty, it falls back to Performance Schema digest data
// ordered by the longest observed execution time.
func ListMySQLDashboardSQL(sourceID string) ([]model.MySQLSlowQuerySample, string, error) {
	ds, err := getRuleDataSourceWithSecret(sourceID)
	if err != nil {
		return nil, "", err
	}
	if !strings.EqualFold(ds.Type, "mysql") {
		return nil, "", errors.New("仅 MySQL 数据源支持 SQL 明细")
	}
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return nil, "", err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	slow, err := listMySQLSlowLog(ctx, db, sourceID)
	if err == nil && len(slow) > 0 {
		return slow, "slow", nil
	}
	history, historyErr := listMySQLStatementHistory(ctx, db, sourceID)
	if historyErr == nil && len(history) > 0 {
		return history, "top", nil
	}
	top, topErr := listMySQLTopStatements(ctx, db, sourceID)
	if topErr != nil {
		return nil, "", topErr
	}
	return top, "top", nil
}

func listMySQLStatementHistory(ctx context.Context, db sqlQueryer, sourceID string) ([]model.MySQLSlowQuerySample, error) {
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(CURRENT_SCHEMA, ''), COALESCE(SQL_TEXT, ''),
		TIMER_WAIT / 1000000000, ROWS_EXAMINED, ROWS_SENT
		FROM performance_schema.events_statements_history_long
		WHERE SQL_TEXT IS NOT NULL AND SQL_TEXT <> ''
		ORDER BY TIMER_WAIT DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.MySQLSlowQuerySample, 0, 50)
	for rows.Next() {
		var item model.MySQLSlowQuerySample
		if err := rows.Scan(&item.SchemaName, &item.QueryText, &item.MaxLatencyMs, &item.RowsExamined, &item.RowsSent); err != nil {
			return nil, err
		}
		item.SourceID = sourceID
		item.Count = 1
		item.TotalLatencyMs = item.MaxLatencyMs
		item.AverageLatencyMs = item.MaxLatencyMs
		items = append(items, item)
	}
	return items, rows.Err()
}

func listMySQLSlowLog(ctx context.Context, db sqlQueryer, sourceID string) ([]model.MySQLSlowQuerySample, error) {
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(db, ''), COALESCE(sql_text, ''),
		TIME_TO_SEC(query_time) * 1000, TIME_TO_SEC(lock_time) * 1000,
		rows_examined, rows_sent, start_time
		FROM mysql.slow_log ORDER BY start_time DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.MySQLSlowQuerySample, 0, 50)
	for rows.Next() {
		var item model.MySQLSlowQuerySample
		var queryTime, lockTime float64
		if err := rows.Scan(&item.SchemaName, &item.QueryText, &queryTime, &lockTime, &item.RowsExamined, &item.RowsSent, &item.LastSeen); err != nil {
			return nil, err
		}
		item.SourceID = sourceID
		item.Count = 1
		item.TotalLatencyMs = queryTime
		item.AverageLatencyMs = queryTime
		item.MaxLatencyMs = queryTime
		item.FirstSeen = item.LastSeen
		items = append(items, item)
	}
	return items, rows.Err()
}

func listMySQLTopStatements(ctx context.Context, db sqlQueryer, sourceID string) ([]model.MySQLSlowQuerySample, error) {
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(SCHEMA_NAME, ''), COALESCE(DIGEST, ''), COALESCE(DIGEST_TEXT, ''), COUNT_STAR,
		SUM_TIMER_WAIT / 1000000000, AVG_TIMER_WAIT / 1000000000, MAX_TIMER_WAIT / 1000000000,
		SUM_ROWS_EXAMINED, SUM_ROWS_SENT, FIRST_SEEN, LAST_SEEN
		FROM performance_schema.events_statements_summary_by_digest
		WHERE DIGEST_TEXT IS NOT NULL AND DIGEST_TEXT <> ''
		ORDER BY MAX_TIMER_WAIT DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.MySQLSlowQuerySample, 0, 50)
	for rows.Next() {
		var item model.MySQLSlowQuerySample
		if err := rows.Scan(&item.SchemaName, &item.Digest, &item.QueryText, &item.Count, &item.TotalLatencyMs,
			&item.AverageLatencyMs, &item.MaxLatencyMs, &item.RowsExamined, &item.RowsSent, &item.FirstSeen, &item.LastSeen); err != nil {
			return nil, err
		}
		item.SourceID = sourceID
		items = append(items, item)
	}
	return items, rows.Err()
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
