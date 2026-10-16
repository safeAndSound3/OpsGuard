package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"monitor-platform/internal/model"
)

type PostgreSQLTablePreview struct {
	Columns    []string `json:"columns"`
	Rows       [][]any  `json:"rows"`
	Page       int      `json:"page"`
	PageSize   int      `json:"pageSize"`
	Total      int      `json:"total"`
	TotalExact bool     `json:"totalExact"`
}

type PostgreSQLStatementResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Message   string   `json:"message"`
	Truncated bool     `json:"truncated"`
}

const maxPostgreSQLQueryRows = 1000

// ExecutePostgreSQLStatement runs read-only SQL (SELECT / SHOW / DESC /
// DESCRIBE / EXPLAIN, including WITH...SELECT) against a PostgreSQL data
// source. Any statement whose first keyword is not read-only is rejected up
// front. lib/pq does not batch multi-statement scripts, so the script is split
// on statement boundaries and each item runs on its own connection.
func ExecutePostgreSQLStatement(sourceID, database, statement string) (PostgreSQLStatementResult, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !ds.Enabled {
		return PostgreSQLStatementResult{}, errors.New("PostgreSQL 数据源不可用")
	}
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return PostgreSQLStatementResult{}, errors.New("请输入要执行的 SQL")
	}
	items := splitSQLScript(statement)
	var last PostgreSQLStatementResult
	for _, item := range items {
		keyword := mysqlStatementKeyword(item)
		switch keyword {
		case "SELECT", "SHOW", "DESC", "DESCRIBE", "EXPLAIN":
		default:
			return PostgreSQLStatementResult{}, errors.New("PostgreSQL 工作台仅支持只读查询（SELECT / SHOW / DESC / EXPLAIN），已拒绝该语句")
		}
		result, err := runPostgreSQLQuery(ds, database, item)
		if err != nil {
			recordSQLHistory(sourceID, database, statement, "failed", 0, 0, 0, err.Error(), "")
			return PostgreSQLStatementResult{}, err
		}
		last = result
	}
	if last.Columns == nil {
		last.Columns = []string{}
	}
	if last.Rows == nil {
		last.Rows = [][]any{}
	}
	if len(last.Columns) == 0 && len(last.Rows) == 0 {
		last.Message = "执行成功"
	}
	recordSQLHistory(sourceID, database, statement, "success", len(last.Rows), 0, 0, "", "")
	return last, nil
}

func runPostgreSQLQuery(ds model.DataSource, database, statement string) (PostgreSQLStatementResult, error) {
	db, err := openPostgreSQLDataSource(ds, database)
	if err != nil {
		return PostgreSQLStatementResult{}, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return PostgreSQLStatementResult{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return PostgreSQLStatementResult{}, err
	}
	data := make([][]any, 0, 100)
	for len(data) < maxPostgreSQLQueryRows && rows.Next() {
		values := make([]any, len(columns))
		refs := make([]any, len(columns))
		for i := range values {
			refs[i] = &values[i]
		}
		if err := rows.Scan(refs...); err != nil {
			return PostgreSQLStatementResult{}, err
		}
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				values[i] = string(bytes)
			}
		}
		data = append(data, values)
	}
	if err := rows.Err(); err != nil {
		return PostgreSQLStatementResult{}, err
	}
	return PostgreSQLStatementResult{Columns: columns, Rows: data, Truncated: len(data) >= maxPostgreSQLQueryRows}, nil
}

func postgresDatabase(ds model.DataSource, database string) string {
	if database = strings.TrimSpace(database); database != "" {
		return database
	}
	if database = primaryDatabase(ds.Database); database != "" {
		return database
	}
	return "postgres"
}

func openPostgreSQLDataSource(ds model.DataSource, database string) (*sql.DB, error) {
	if err := FillDataSourcePassword(&ds); err != nil && strings.TrimSpace(ds.Password) == "" {
		return nil, err
	}
	port := strings.TrimSpace(ds.Port)
	if port == "" {
		port = "5432"
	}
	sslMode := strings.TrimSpace(ds.Options["sslmode"])
	if sslMode == "" {
		sslMode = "disable"
	}
	dsnURL := &url.URL{Scheme: "postgres", User: url.UserPassword(ds.Username, ds.Password), Host: net.JoinHostPort(ds.Host, port), Path: "/" + postgresDatabase(ds, database), RawQuery: url.Values{"sslmode": []string{sslMode}}.Encode()}
	dsn := dsnURL.String()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func TestPostgreSQLDataSource(ds model.DataSource) error {
	db, err := openPostgreSQLDataSource(ds, "")
	if err != nil {
		return fmt.Errorf("PostgreSQL 连接失败：%w", err)
	}
	return db.Close()
}

func ListPostgreSQLDatabases(sourceID string) ([]string, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return nil, err
	}
	db, err := openPostgreSQLDataSource(ds, "postgres")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT datname FROM pg_database WHERE datallowconn = true AND NOT datistemplate ORDER BY datname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			items = append(items, name)
		}
	}
	return items, rows.Err()
}

func ListPostgreSQLTables(sourceID, database string) ([]string, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return nil, err
	}
	db, err := openPostgreSQLDataSource(ds, database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT table_schema || '.' || table_name FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			items = append(items, name)
		}
	}
	return items, rows.Err()
}

func PreviewPostgreSQLTable(sourceID, database, table string, page, pageSize int) (PostgreSQLTablePreview, error) {
	if strings.TrimSpace(table) == "" {
		return PostgreSQLTablePreview{}, fmt.Errorf("PostgreSQL 数据表不能为空")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 500 {
		pageSize = 50
	}
	parts := strings.SplitN(table, ".", 2)
	schema, tableName := "public", parts[0]
	if len(parts) == 2 {
		schema, tableName = parts[0], parts[1]
	}
	if !validSQLIdentifier(schema) || !validSQLIdentifier(tableName) {
		return PostgreSQLTablePreview{}, fmt.Errorf("数据表名称无效")
	}
	ds, err := GetDataSourceByID(sourceID)
	if err != nil {
		return PostgreSQLTablePreview{}, err
	}
	db, err := openPostgreSQLDataSource(ds, database)
	if err != nil {
		return PostgreSQLTablePreview{}, err
	}
	defer db.Close()
	query := fmt.Sprintf(`SELECT * FROM "%s"."%s" LIMIT $1 OFFSET $2`, strings.ReplaceAll(schema, `"`, `""`), strings.ReplaceAll(tableName, `"`, `""`))
	var total int64
	if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"."%s"`, strings.ReplaceAll(schema, `"`, `""`), strings.ReplaceAll(tableName, `"`, `""`))).Scan(&total); err != nil {
		return PostgreSQLTablePreview{}, err
	}
	rows, err := db.Query(query, pageSize, (page-1)*pageSize)
	if err != nil {
		return PostgreSQLTablePreview{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return PostgreSQLTablePreview{}, err
	}
	result := PostgreSQLTablePreview{Columns: columns, Rows: [][]any{}, Page: page, PageSize: pageSize, Total: int(total), TotalExact: true}
	for rows.Next() {
		values := make([]any, len(columns))
		refs := make([]any, len(columns))
		for i := range values {
			refs[i] = &values[i]
		}
		if err := rows.Scan(refs...); err != nil {
			return PostgreSQLTablePreview{}, err
		}
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				values[i] = string(bytes)
			}
		}
		result.Rows = append(result.Rows, values)
	}
	return result, rows.Err()
}
