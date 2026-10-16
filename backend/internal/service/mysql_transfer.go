package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxMySQLTransferBytes = 20 * 1024 * 1024
const maxMySQLTransferRows = 100000
const maxMySQLImportStatements = 1000

type MySQLTransferOptions struct {
	Database string
	Table    string
	Scope    string // structure, schema, data
	Format   string // sql, csv, json
}

type MySQLImportStatementResult struct {
	Index     int    `json:"index"`
	Statement string `json:"statement"`
	Status    string `json:"status"`
	Affected  int64  `json:"affected"`
	Error     string `json:"error,omitempty"`
}

type MySQLImportResult struct {
	Affected  int64                        `json:"affected"`
	Completed bool                         `json:"completed"`
	Results   []MySQLImportStatementResult `json:"results"`
}

type mysqlTransferDocument struct {
	Database       string   `json:"database"`
	Table          string   `json:"table"`
	CreateDatabase string   `json:"createDatabase,omitempty"`
	CreateTable    string   `json:"createTable,omitempty"`
	Columns        []string `json:"columns,omitempty"`
	Rows           [][]any  `json:"rows,omitempty"`
	Scope          string   `json:"scope"`
	ExportedAt     string   `json:"exportedAt"`
}

func ExportMySQLData(sourceID string, options MySQLTransferOptions) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exportMySQLDataContext(ctx, sourceID, options)
}

func exportMySQLDataContext(ctx context.Context, sourceID string, options MySQLTransferOptions) ([]byte, string, error) {
	options.Database = strings.TrimSpace(options.Database)
	options.Table = strings.TrimSpace(options.Table)
	options.Scope = strings.TrimSpace(options.Scope)
	options.Format = strings.ToLower(strings.TrimSpace(options.Format))
	if options.Database == "" {
		return nil, "", errors.New("请选择有效的数据库")
	}
	if options.Scope != "structure" && options.Scope != "schema" && options.Scope != "data" {
		return nil, "", errors.New("invalid export scope")
	}
	if options.Format != "sql" && options.Format != "csv" && options.Format != "json" {
		return nil, "", errors.New("invalid export format")
	}
	if options.Format == "csv" && options.Scope != "data" {
		return nil, "", errors.New("CSV 仅支持导出库表数据")
	}
	if options.Table == "" {
		if options.Format != "sql" {
			return nil, "", errors.New("未选择表时仅支持导出 SQL")
		}
		return exportMySQLDatabaseSQL(ctx, sourceID, options)
	}
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(ds.Type, "mysql") || !ds.Enabled {
		return nil, "", errors.New("MySQL data source not available")
	}
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return nil, "", err
	}
	defer db.Close()
	createDatabase, err := mysqlShowCreate(ctx, db, "DATABASE", options.Database)
	if err != nil {
		return nil, "", err
	}
	createTable, err := mysqlShowCreateTable(ctx, db, options.Database, options.Table)
	if err != nil {
		return nil, "", err
	}
	doc := mysqlTransferDocument{Database: options.Database, Table: options.Table, Scope: options.Scope, ExportedAt: time.Now().Format(time.RFC3339), CreateDatabase: createDatabase, CreateTable: createTable}
	if options.Scope == "data" {
		doc.Columns, doc.Rows, err = mysqlTransferRows(ctx, db, options.Database, options.Table)
		if err != nil {
			return nil, "", err
		}
	}
	base := sanitizeMySQLTransferName(options.Database + "_" + options.Table)
	switch options.Format {
	case "json":
		content, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, "", err
		}
		if len(content) > maxMySQLTransferBytes {
			return nil, "", errors.New("导出内容超过 20 MB，请缩小范围")
		}
		return content, base + ".json", nil
	case "csv":
		var buffer bytes.Buffer
		buffer.WriteString("\xEF\xBB\xBF")
		writer := csv.NewWriter(&buffer)
		if err := writer.Write(safeCSVRecord(doc.Columns)); err != nil {
			return nil, "", err
		}
		for _, row := range doc.Rows {
			record := make([]string, len(row))
			for index, value := range row {
				record[index] = safeCSVCell(mysqlTransferString(value))
			}
			if err := writer.Write(record); err != nil {
				return nil, "", err
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return nil, "", err
		}
		return buffer.Bytes(), base + ".csv", nil
	default:
		var buffer bytes.Buffer
		if options.Scope == "schema" || options.Scope == "data" {
			buffer.WriteString(createDatabase + ";\n\n")
		}
		buffer.WriteString(fmt.Sprintf("USE %s;\n\n", mysqlQuoteIdentifier(options.Database)))
		buffer.WriteString(createTable + ";\n")
		if options.Scope == "data" {
			for _, row := range doc.Rows {
				buffer.WriteString(fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s);\n", options.Table, mysqlTransferColumns(doc.Columns), mysqlTransferValues(row)))
				if buffer.Len() > maxMySQLTransferBytes {
					return nil, "", errors.New("导出内容超过 20 MB，请缩小范围")
				}
			}
		}
		return buffer.Bytes(), base + ".sql", nil
	}
}

func exportMySQLDatabaseSQL(ctx context.Context, sourceID string, options MySQLTransferOptions) ([]byte, string, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(ds.Type, "mysql") || !ds.Enabled {
		return nil, "", errors.New("MySQL 数据源不可用")
	}
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return nil, "", err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SHOW FULL TABLES FROM "+mysqlQuoteIdentifier(options.Database)+" WHERE Table_type = 'BASE TABLE'")
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, "", err
		}
		tables = append(tables, name)
	}
	var out strings.Builder
	if options.Scope == "schema" || options.Scope == "data" {
		createDB, e := mysqlShowCreate(ctx, db, "DATABASE", options.Database)
		if e != nil {
			return nil, "", e
		}
		out.WriteString(createDB + ";\n\n")
	}
	for _, table := range tables {
		create, e := mysqlShowCreateTable(ctx, db, options.Database, table)
		if e != nil {
			return nil, "", e
		}
		out.WriteString("USE `" + options.Database + "`;\n\n" + create + ";\n")
		if options.Scope == "data" {
			cols, data, e := mysqlTransferRows(ctx, db, options.Database, table)
			if e != nil {
				return nil, "", e
			}
			for _, row := range data {
				out.WriteString(fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s);\n", table, mysqlTransferColumns(cols), mysqlTransferValues(row)))
				if out.Len() > maxMySQLTransferBytes {
					return nil, "", errors.New("导出内容超过 20 MB，请缩小范围")
				}
			}
		}
		out.WriteString("\n")
	}
	return []byte(out.String()), sanitizeMySQLTransferName(options.Database) + ".sql", nil
}

// importMySQLDataContext permits long-running jobs to report per-statement progress
// and honor a cancelled request rather than outliving the user action.
func importMySQLDataContext(ctx context.Context, sourceID, database, table, format string, content []byte, progress func(int, string)) (MySQLImportResult, error) {
	if len(content) == 0 || len(content) > maxMySQLTransferBytes {
		return MySQLImportResult{}, errors.New("导入文件必须在 20 MB 以内")
	}
	format = strings.ToLower(strings.TrimSpace(format))
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(ds.Type, "mysql") || !ds.Enabled {
		return MySQLImportResult{}, errors.New("MySQL data source not available")
	}
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return MySQLImportResult{}, err
	}
	defer db.Close()
	if format == "sql" {
		// 研发常从 Markdown/聊天窗口复制 SQL，清理代码围栏和转义下划线后再解析。
		sqlContent := strings.ReplaceAll(string(content), "\\_", "_")
		sqlContent = strings.ReplaceAll(sqlContent, "```sql", "")
		sqlContent = strings.ReplaceAll(sqlContent, "```SQL", "")
		sqlContent = strings.ReplaceAll(sqlContent, "```", "")
		statements, splitErr := splitMySQLStatements(sqlContent)
		if splitErr != nil {
			return MySQLImportResult{}, splitErr
		}
		if len(statements) == 0 {
			return MySQLImportResult{}, errors.New("SQL 文件中没有可执行语句")
		}
		response := MySQLImportResult{Results: make([]MySQLImportStatementResult, 0, len(statements))}
		for index, statement := range statements {
			if err := ctx.Err(); err != nil {
				return response, err
			}
			item := MySQLImportStatementResult{Index: index + 1, Statement: mysqlTransferStatementPreview(statement)}
			result, err := db.ExecContext(ctx, statement)
			if err != nil {
				item.Status = "failed"
				item.Error = mysqlTransferErrorPreview(err)
				response.Results = append(response.Results, item)
				for skipped := index + 1; skipped < len(statements); skipped++ {
					response.Results = append(response.Results, MySQLImportStatementResult{Index: skipped + 1, Statement: mysqlTransferStatementPreview(statements[skipped]), Status: "skipped"})
				}
				return response, fmt.Errorf("第 %d 条 SQL 执行失败：%s", index+1, item.Error)
			}
			count, _ := result.RowsAffected()
			item.Status, item.Affected = "success", count
			response.Affected += count
			response.Results = append(response.Results, item)
			if progress != nil {
				progress((index+1)*100/len(statements), fmt.Sprintf("已执行 %d/%d 条语句", index+1, len(statements)))
			}
		}
		clearMySQLTableCountCache()
		response.Completed = true
		return response, nil
	}
	if format == "json" {
		var document mysqlTransferDocument
		if err := json.Unmarshal(content, &document); err != nil {
			return MySQLImportResult{}, errors.New("JSON 文件格式无效")
		}
		if database == "" {
			database = document.Database
		}
		if table == "" {
			table = document.Table
		}
		if progress != nil {
			progress(20, "正在写入 JSON 数据")
		}
		affected, err := importMySQLRows(ctx, db, database, table, document.Columns, document.Rows)
		if progress != nil && err == nil {
			progress(100, "JSON 数据写入完成")
		}
		return MySQLImportResult{Affected: affected, Completed: err == nil, Results: []MySQLImportStatementResult{{Index: 1, Statement: "写入 JSON 数据", Status: mysqlTransferStatus(err), Affected: affected, Error: mysqlTransferErrorPreview(err)}}}, err
	}
	if format == "csv" {
		reader := csv.NewReader(bytes.NewReader(content))
		reader.FieldsPerRecord = -1
		columns, err := reader.Read()
		if err != nil {
			return MySQLImportResult{}, errors.New("CSV 缺少表头")
		}
		rows := make([][]any, 0)
		for len(rows) < maxMySQLTransferRows {
			record, readErr := reader.Read()
			if readErr != nil {
				if readErr.Error() == "EOF" {
					break
				}
				return MySQLImportResult{}, readErr
			}
			row := make([]any, len(record))
			for index := range record {
				row[index] = record[index]
			}
			rows = append(rows, row)
		}
		if progress != nil {
			progress(20, "正在写入 CSV 数据")
		}
		affected, err := importMySQLRows(ctx, db, database, table, columns, rows)
		if progress != nil && err == nil {
			progress(100, "CSV 数据写入完成")
		}
		return MySQLImportResult{Affected: affected, Completed: err == nil, Results: []MySQLImportStatementResult{{Index: 1, Statement: "写入 CSV 数据", Status: mysqlTransferStatus(err), Affected: affected, Error: mysqlTransferErrorPreview(err)}}}, err
	}
	return MySQLImportResult{}, errors.New("仅支持 SQL、CSV、JSON 文件")
}

func mysqlTransferStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "success"
}
func mysqlTransferErrorPreview(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 500 {
		return value[:500] + "..."
	}
	return value
}
func mysqlTransferStatementPreview(statement string) string {
	value := redactSQLForHistory(statement)
	if len(value) > 500 {
		return value[:500] + "..."
	}
	return value
}

func mysqlShowCreate(ctx context.Context, db *sql.DB, kind, name string) (string, error) {
	var ignored, statement string
	err := db.QueryRowContext(ctx, "SHOW CREATE "+kind+" "+mysqlQuoteIdentifier(name)).Scan(&ignored, &statement)
	return statement, err
}
func mysqlShowCreateTable(ctx context.Context, db *sql.DB, database, table string) (string, error) {
	var ignored, statement string
	err := db.QueryRowContext(ctx, fmt.Sprintf("SHOW CREATE TABLE %s.%s", mysqlQuoteIdentifier(database), mysqlQuoteIdentifier(table))).Scan(&ignored, &statement)
	return statement, err
}
func mysqlTransferRows(ctx context.Context, db *sql.DB, database, table string) ([]string, [][]any, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s.%s LIMIT %d", mysqlQuoteIdentifier(database), mysqlQuoteIdentifier(table), maxMySQLTransferRows))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	result := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		refs := make([]any, len(columns))
		for i := range values {
			refs[i] = &values[i]
		}
		if err := rows.Scan(refs...); err != nil {
			return nil, nil, err
		}
		for i := range values {
			values[i] = mysqlJSONValue(values[i])
		}
		result = append(result, values)
	}
	return columns, result, rows.Err()
}
func mysqlTransferColumns(columns []string) string {
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = "`" + strings.ReplaceAll(column, "`", "``") + "`"
	}
	return strings.Join(quoted, ", ")
}
func mysqlTransferValues(row []any) string {
	values := make([]string, len(row))
	for i, value := range row {
		if value == nil {
			values[i] = "NULL"
		} else {
			values[i] = "'" + strings.ReplaceAll(mysqlTransferString(value), "'", "''") + "'"
		}
	}
	return strings.Join(values, ", ")
}
func mysqlTransferString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case nil:
		return "NULL"
	default:
		return fmt.Sprint(typed)
	}
}
func sanitizeMySQLTransferName(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
}
func importMySQLRows(ctx context.Context, db *sql.DB, database, table string, columns []string, rows [][]any) (int64, error) {
	if !validSQLIdentifier(database) || !validSQLIdentifier(table) || len(columns) == 0 {
		return 0, errors.New("请选择有效的目标库和表")
	}
	for _, column := range columns {
		if !validSQLIdentifier(column) {
			return 0, errors.New("导入文件含无效字段")
		}
	}
	if len(rows) > maxMySQLTransferRows {
		return 0, errors.New("导入行数超过 100000 行")
	}
	query := fmt.Sprintf("INSERT INTO `%s`.`%s` (%s) VALUES (%s)", database, table, mysqlTransferColumns(columns), strings.TrimRight(strings.Repeat("?,", len(columns)), ","))
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	var affected int64
	for _, row := range rows {
		if len(row) != len(columns) {
			_ = tx.Rollback()
			return 0, errors.New("导入行与表头字段数量不一致")
		}
		result, execErr := tx.ExecContext(ctx, query, row...)
		if execErr != nil {
			_ = tx.Rollback()
			return 0, execErr
		}
		count, _ := result.RowsAffected()
		affected += count
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	clearMySQLTableCountCache()
	return affected, nil
}
func splitMySQLStatements(content string) ([]string, error) {
	parts := make([]string, 0)
	var current strings.Builder
	quote := byte(0)
	escaped := false
	blockComment, lineComment := false, false
	delimiter := ";"
	for index := 0; index < len(content); {
		if quote == 0 && !blockComment && !lineComment && (index == 0 || content[index-1] == '\n') && strings.HasPrefix(strings.ToUpper(content[index:]), "DELIMITER") {
			lineEnd := strings.IndexByte(content[index:], '\n')
			if lineEnd < 0 {
				lineEnd = len(content) - index
			}
			fields := strings.Fields(content[index : index+lineEnd])
			if len(fields) != 2 || len(fields[1]) > 16 {
				return nil, errors.New("SQL 文件中的 DELIMITER 指令无效")
			}
			delimiter = fields[1]
			index += lineEnd
			if index < len(content) && content[index] == '\n' {
				index++
			}
			continue
		}
		character := content[index]
		if lineComment {
			current.WriteByte(character)
			if character == '\n' {
				lineComment = false
			}
			index++
			continue
		}
		if blockComment {
			current.WriteByte(character)
			if character == '*' && index+1 < len(content) && content[index+1] == '/' {
				current.WriteByte('/')
				index += 2
				blockComment = false
				continue
			}
			index++
			continue
		}
		if quote != 0 {
			current.WriteByte(character)
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == quote {
				quote = 0
			}
			index++
			continue
		}
		if character == '-' && index+2 < len(content) && content[index+1] == '-' && (content[index+2] == ' ' || content[index+2] == '\t') {
			lineComment = true
			current.WriteString("--")
			index += 2
			continue
		}
		if character == '#' {
			lineComment = true
			current.WriteByte(character)
			index++
			continue
		}
		if character == '/' && index+1 < len(content) && content[index+1] == '*' {
			blockComment = true
			current.WriteString("/*")
			index += 2
			continue
		}
		if character == '\'' || character == '"' || character == '`' {
			quote = character
			current.WriteByte(character)
			index++
			continue
		}
		if strings.HasPrefix(content[index:], delimiter) {
			if statement := mysqlSQLWithoutLeadingComments(current.String()); statement != "" {
				parts = append(parts, statement)
				if len(parts) > maxMySQLImportStatements {
					return nil, fmt.Errorf("SQL 文件最多包含 %d 条语句", maxMySQLImportStatements)
				}
			}
			current.Reset()
			index += len(delimiter)
			continue
		}
		current.WriteByte(character)
		index++
	}
	if statement := mysqlSQLWithoutLeadingComments(current.String()); statement != "" {
		parts = append(parts, statement)
		if len(parts) > maxMySQLImportStatements {
			return nil, fmt.Errorf("SQL 文件最多包含 %d 条语句", maxMySQLImportStatements)
		}
	}
	return parts, nil
}

func mysqlSQLWithoutLeadingComments(raw string) string {
	lines := strings.Split(raw, "\n")
	for len(lines) > 0 {
		line := strings.TrimSpace(lines[0])
		if line == "" || strings.HasPrefix(line, "--") || strings.HasPrefix(line, "#") {
			lines = lines[1:]
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
