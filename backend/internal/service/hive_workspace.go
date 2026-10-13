package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beltran/gohive"
)

type HiveTablePreview struct {
	Columns    []string `json:"columns"`
	Rows       [][]any  `json:"rows"`
	Page       int      `json:"page"`
	PageSize   int      `json:"pageSize"`
	Total      int      `json:"total"`
	TotalExact bool     `json:"totalExact"`
}

func hiveConnection(sourceID string) (*gohive.Connection, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(ds.Type, "hive") || !ds.Enabled {
		return nil, fmt.Errorf("Hive 数据源不存在或已停用")
	}
	if err := FillDataSourcePassword(&ds); err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(ds.Port))
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("HiveServer2 端口无效")
	}
	config := gohive.NewConnectConfiguration()
	config.Username = strings.TrimSpace(ds.Username)
	// gohive requires a client username even for NOSASL. It is not used for
	// authentication in that mode, so use a stable fallback when the source
	// intentionally has no username.
	if config.Username == "" {
		config.Username = "hive"
	}
	config.Password = ds.Password
	config.Database = primaryDatabase(ds.Database)
	config.FetchSize = 500
	auth := strings.ToUpper(strings.TrimSpace(ds.Options["auth"]))
	if auth == "" {
		auth = strings.ToUpper(strings.TrimSpace(ds.Options["authentication"]))
	}
	if auth == "" {
		if config.Username != "" || config.Password != "" {
			auth = "NONE"
		} else {
			auth = "NOSASL"
		}
	}
	if auth != "NOSASL" && auth != "NONE" && auth != "LDAP" && auth != "CUSTOM" {
		return nil, fmt.Errorf("HiveServer2 认证方式不支持：%s", auth)
	}
	transport := strings.ToLower(strings.TrimSpace(ds.Options["transport"]))
	if transport == "" {
		transport = strings.ToLower(strings.TrimSpace(ds.Options["transportMode"]))
	}
	if transport == "http" {
		config.TransportMode = "http"
		if path := strings.TrimSpace(ds.Options["httpPath"]); path != "" {
			config.HTTPPath = path
		}
	} else if transport != "" && transport != "binary" {
		return nil, fmt.Errorf("HiveServer2 传输方式不支持：%s", transport)
	}
	conn, err := gohive.Connect(strings.TrimSpace(ds.Host), port, auth, config)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "eof") {
			return nil, fmt.Errorf("HiveServer2 连接失败：服务端已关闭连接，请检查认证方式（%s）、传输方式（%s）和端口", auth, config.TransportMode)
		}
		return nil, fmt.Errorf("HiveServer2 连接失败：%w", err)
	}
	return conn, nil
}

func hiveQuery(sourceID, statement string, maxRows int) ([]string, [][]any, error) {
	if maxRows < 1 || maxRows > 5000 {
		maxRows = 500
	}
	conn, err := hiveConnection(sourceID)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cursor := conn.Cursor()
	cursor.Execute(ctx, statement, false)
	if cursor.Err != nil {
		return nil, nil, formatHiveQueryError(cursor.Err)
	}
	columns := cursor.Description()
	if cursor.Err != nil {
		return nil, nil, formatHiveQueryError(cursor.Err)
	}
	columnNames := make([]string, 0, len(columns))
	for _, column := range columns {
		if len(column) > 0 {
			columnNames = append(columnNames, column[0])
		}
	}
	rows := make([][]any, 0, maxRows)
	for len(rows) < maxRows && cursor.HasMore(ctx) {
		row := cursor.RowMap(ctx)
		if cursor.Err != nil {
			return nil, nil, formatHiveQueryError(cursor.Err)
		}
		values := make([]any, 0, len(columnNames))
		for _, name := range columnNames {
			values = append(values, row[name])
		}
		rows = append(rows, values)
	}
	// HiveServer2 prefixes column names with the table name ("tbl.col"). That
	// qualified form is invalid inside SQL we generate ourselves (Semantic
	//Exception 10004), so strip it down to the last dotted segment when that
	// segment is a plain identifier. Data extraction above still uses the raw
	// qualified names as RowMap keys.
	cleanNames := make([]string, 0, len(columnNames))
	for _, name := range columnNames {
		cleanNames = append(cleanNames, cleanHiveColumn(name))
	}
	return cleanNames, rows, nil
}

// cleanHiveColumn reduces a HiveServer2 column name like "tbl.col" to "col"
// when the last dotted segment is a safe bare identifier; otherwise it keeps
// the name as-is so genuinely dotted (backtick-quoted) columns still surface.
func cleanHiveColumn(name string) string {
	trimmed := strings.TrimSpace(name)
	if idx := strings.LastIndex(trimmed, "."); idx > 0 {
		last := trimmed[idx+1:]
		if plainIdentifier(last) {
			return last
		}
	}
	return trimmed
}

func formatHiveQueryError(err error) error {
	if err == nil {
		return nil
	}
	var hiveErr gohive.HiveError
	if errors.As(err, &hiveErr) {
		message := strings.TrimSpace(hiveErr.Message)
		if message != "" {
			return fmt.Errorf("HiveServer2 执行查询失败（错误码 %d）：%s", hiveErr.ErrorCode, message)
		}
		return fmt.Errorf("HiveServer2 执行查询失败（错误码 %d）：%s", hiveErr.ErrorCode, hiveErr.Error())
	}
	return err
}

func hiveIdentifier(value string) string {
	return "`" + strings.ReplaceAll(strings.TrimSpace(value), "`", "``") + "`"
}

func ListHiveDatabases(sourceID string) ([]string, error) {
	_, rows, err := hiveQuery(sourceID, "SHOW DATABASES", 5000)
	if err != nil {
		return nil, err
	}
	items := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) > 0 && strings.TrimSpace(fmt.Sprint(row[0])) != "" {
			items = append(items, fmt.Sprint(row[0]))
		}
	}
	return items, nil
}

func ListHiveTables(sourceID, database string) ([]string, error) {
	if strings.TrimSpace(database) == "" {
		return nil, fmt.Errorf("Hive 数据库不能为空")
	}
	_, rows, err := hiveQuery(sourceID, "SHOW TABLES IN "+hiveIdentifier(database), 5000)
	if err != nil {
		return nil, err
	}
	items := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) > 0 && strings.TrimSpace(fmt.Sprint(row[0])) != "" {
			items = append(items, fmt.Sprint(row[0]))
		}
	}
	return items, nil
}

func ListHivePartitions(sourceID, database, table string) ([]string, error) {
	if strings.TrimSpace(database) == "" || strings.TrimSpace(table) == "" {
		return nil, fmt.Errorf("Hive 数据库和表不能为空")
	}
	_, rows, err := hiveQuery(sourceID, "SHOW PARTITIONS "+hiveIdentifier(database)+"."+hiveIdentifier(table), 5000)
	if err != nil {
		return nil, err
	}
	items := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) > 0 {
			items = append(items, fmt.Sprint(row[0]))
		}
	}
	return items, nil
}

func PreviewHiveTable(sourceID, database, table, partition string, page, pageSize int) (HiveTablePreview, error) {
	if strings.TrimSpace(database) == "" || strings.TrimSpace(table) == "" {
		return HiveTablePreview{}, fmt.Errorf("Hive 数据库和表不能为空")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 2000 {
		pageSize = 100
	}
	base := hiveIdentifier(database) + "." + hiveIdentifier(table)
	where := ""
	if strings.TrimSpace(partition) != "" {
		parts := strings.Split(partition, "/")
		conditions := make([]string, 0, len(parts))
		for _, item := range parts {
			pair := strings.SplitN(item, "=", 2)
			if len(pair) != 2 || strings.TrimSpace(pair[0]) == "" {
				return HiveTablePreview{}, fmt.Errorf("分区格式无效")
			}
			conditions = append(conditions, hiveIdentifier(pair[0])+" = '"+strings.ReplaceAll(pair[1], "'", "''")+"'")
		}
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	// Data first: page rows are fetched before the optional row count so a slow
	// full-table COUNT(*) never blocks the visible preview.
	// Hive/Spark do not reliably support "LIMIT n OFFSET m" (older versions reject
	// OFFSET with an "invalid column reference tok_allcolref" error), so paging is
	// implemented with an explicit-column ROW_NUMBER() subquery instead.
	probeStmt := "SELECT * FROM " + base + where + " LIMIT 1"
	columns, probeRows, err := hiveQuery(sourceID, probeStmt, 1)
	if err != nil {
		return HiveTablePreview{}, err
	}
	if len(probeRows) == 0 {
		total := 0
		totalExact := false
		if count, err := countHiveRows(sourceID, "SELECT COUNT(*) FROM "+base+where); err == nil {
			total = count
			totalExact = true
		}
		return HiveTablePreview{Columns: columns, Rows: [][]any{}, Page: page, PageSize: pageSize, Total: total, TotalExact: totalExact}, nil
	}
	quotedCols := hiveQuoteColumns(columns)
	colList := strings.Join(quotedCols, ", ")
	orderCol := quotedCols[0]
	offset := (page - 1) * pageSize
	// Hive reliably supports the comma form "LIMIT offset, rows" (0.13+), while
	// both "LIMIT n OFFSET m" and ROW_NUMBER() OVER() inside a multi-column
	// subquery select list fail to parse on Hive 3.1.3. ORDER BY keeps the page
	// window stable across calls.
	statement := "SELECT " + colList + " FROM " + base + where + " ORDER BY " + orderCol + " LIMIT " + strconv.Itoa(offset) + ", " + strconv.Itoa(pageSize)
	columns, rows, err := hiveQuery(sourceID, statement, pageSize)
	if err != nil {
		return HiveTablePreview{}, err
	}
	// Row count is best-effort: Hive COUNT(*) on large tables can take minutes,
	// so it runs on its own short timeout and degrades to an inexact total.
	total := len(rows)
	totalExact := false
	if count, err := countHiveRows(sourceID, "SELECT COUNT(*) FROM "+base+where); err == nil {
		total = count
		totalExact = true
	}
	return HiveTablePreview{Columns: columns, Rows: rows, Page: page, PageSize: pageSize, Total: total, TotalExact: totalExact}, nil
}

func hiveQuoteColumns(names []string) []string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if plainIdentifier(trimmed) {
			// Hive 3.1.3's Calcite parser fails ("cannot recognize input near AS")
			// when a window function shares a select list with several
			// backtick-quoted columns, so plain names are left unquoted.
			quoted = append(quoted, trimmed)
		} else {
			quoted = append(quoted, "`"+strings.ReplaceAll(trimmed, "`", "``")+"`")
		}
	}
	return quoted
}

// plainIdentifier reports whether s is a safe bare SQL identifier
// (letters/digits/underscore, not starting with a digit).
func plainIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// countHiveRows executes a single-column COUNT query with a short timeout. It
// is intentionally independent from hiveQuery (120s) so a heavy table does not
// stall the preview response.
func countHiveRows(sourceID, statement string) (int, error) {
	conn, err := hiveConnection(sourceID)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cursor := conn.Cursor()
	cursor.Execute(ctx, statement, false)
	if cursor.Err != nil {
		return 0, formatHiveQueryError(cursor.Err)
	}
	if cursor.HasMore(ctx) {
		row := cursor.RowMap(ctx)
		if cursor.Err != nil {
			return 0, formatHiveQueryError(cursor.Err)
		}
		for _, value := range row {
			return int(atoi(fmt.Sprint(value))), nil
		}
	}
	return 0, nil
}

func atoi(v string) int64 {
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' { break }
		n = n*10 + int(c-'0')
	}
	return int64(n)
}

type HiveStatementResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Message   string   `json:"message"`
	Truncated bool     `json:"truncated"`
}

const maxHiveQueryRows = 1000

// ExecuteHiveStatement runs read-only SQL (SELECT / SHOW / DESC / DESCRIBE /
// EXPLAIN, including WITH...SELECT) against a Hive data source. Any statement
// whose first keyword is not read-only is rejected up front.
func ExecuteHiveStatement(sourceID, database, statement string) (HiveStatementResult, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(ds.Type, "hive") || !ds.Enabled {
		return HiveStatementResult{}, errors.New("Hive 数据源不可用")
	}
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return HiveStatementResult{}, errors.New("请输入要执行的 SQL")
	}
	items := splitSQLScript(statement)
	var last HiveStatementResult
	for _, item := range items {
		keyword := mysqlStatementKeyword(item)
		switch keyword {
		case "SELECT", "SHOW", "DESC", "DESCRIBE", "EXPLAIN":
		default:
			return HiveStatementResult{}, errors.New("Hive 工作台仅支持只读查询（SELECT / SHOW / DESC / EXPLAIN），已拒绝该语句")
		}
		columns, rows, err := hiveQuery(sourceID, item, maxHiveQueryRows)
		if err != nil {
			return HiveStatementResult{}, err
		}
		last = HiveStatementResult{Columns: columns, Rows: rows, Truncated: len(rows) >= maxHiveQueryRows}
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
	return last, nil
}
