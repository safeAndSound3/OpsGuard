package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type MySQLTablePreview struct {
	Columns    []string `json:"columns"`
	Rows       [][]any  `json:"rows"`
	Page       int      `json:"page"`
	PageSize   int      `json:"pageSize"`
	Total      int64    `json:"total"`
	TotalPages int      `json:"totalPages"`
	TotalExact bool     `json:"totalExact"`
}

type MySQLTablePreviewOptions struct {
	FilterColumn  string
	FilterValue   string
	SortColumn    string
	SortDirection string
}

type mysqlTableCountCacheEntry struct {
	total     int64
	exact     bool
	expiresAt time.Time
}

var mysqlTableCountCache sync.Map

type MySQLColumnDetail struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Collation  *string `json:"collation"`
	Nullable   bool    `json:"nullable"`
	Key        string  `json:"key"`
	Default    *string `json:"default"`
	Extra      string  `json:"extra"`
	Privileges string  `json:"privileges"`
	Comment    string  `json:"comment"`
}

type MySQLStatementResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Affected  int64    `json:"affected"`
	Message   string   `json:"message"`
	Truncated bool     `json:"truncated"`
}

type MySQLMutationResult struct {
	Affected int64 `json:"affected"`
}

type MySQLSQLHistory struct {
	ID         int64  `json:"id"`
	SourceID   string `json:"sourceId"`
	Database   string `json:"database"`
	Statement  string `json:"statement"`
	Status     string `json:"status"`
	ResultRows int    `json:"resultRows"`
	Affected   int64  `json:"affected"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
	Username   string `json:"username"`
	CreatedAt  string `json:"createdAt"`
}

var errMySQLWriteConfirmation = errors.New("write operations require confirmation")

func initMySQLWorkspaceStore(current *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := current.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS mysql_sql_history (
		id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
		source_id varchar(64) NOT NULL,
		database_name varchar(128) NOT NULL DEFAULT '',
		statement_text mediumtext NOT NULL,
		status varchar(16) NOT NULL,
		result_rows int NOT NULL DEFAULT 0,
		affected_rows bigint NOT NULL DEFAULT 0,
		duration_ms bigint NOT NULL DEFAULT 0,
		error_message varchar(1000) NOT NULL DEFAULT '',
		username varchar(64) NOT NULL DEFAULT '',
		created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_mysql_sql_history_source (source_id, id)
	)`)
	return err
}

func ExecuteMySQLStatement(sourceID, database, statement, username string, confirmed bool) (MySQLStatementResult, error) {
	started := time.Now()
	result, err := executeMySQLStatement(sourceID, database, statement, confirmed)
	if !errors.Is(err, errMySQLWriteConfirmation) {
		recordMySQLSQLHistory(sourceID, strings.TrimSpace(database), redactSQLForHistory(statement), strings.TrimSpace(username), result, err, time.Since(started))
	}
	return result, err
}

func executeMySQLStatement(sourceID, database, statement string, confirmed bool) (MySQLStatementResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return executeMySQLStatementContext(ctx, sourceID, database, statement, confirmed)
}

func executeMySQLStatementContext(ctx context.Context, sourceID, database, statement string, confirmed bool) (MySQLStatementResult, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(ds.Type, "mysql") || !ds.Enabled {
		return MySQLStatementResult{}, errors.New("MySQL data source not available")
	}
	database = strings.TrimSpace(database)
	if database != "" {
		if !validSQLIdentifier(database) {
			return MySQLStatementResult{}, errors.New("invalid database name")
		}
		ds.Database = database
	}
	statement = strings.TrimSpace(statement)
	if statement == "" || strings.Contains(strings.TrimSuffix(statement, ";"), ";") {
		return MySQLStatementResult{}, errors.New("only one SQL statement is allowed")
	}
	normalized := mysqlStatementKeyword(statement)
	read := normalized == "SELECT" || normalized == "SHOW" || normalized == "DESCRIBE" || normalized == "DESC" || normalized == "EXPLAIN"
	if !read && GetOperationConfirmations() && !confirmed {
		return MySQLStatementResult{}, errMySQLWriteConfirmation
	}
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return MySQLStatementResult{}, err
	}
	defer db.Close()
	if !read {
		result, err := db.ExecContext(ctx, statement)
		if err != nil {
			return MySQLStatementResult{}, err
		}
		affected, _ := result.RowsAffected()
		clearMySQLTableCountCache()
		return MySQLStatementResult{Affected: affected, Message: "executed"}, nil
	}
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return MySQLStatementResult{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return MySQLStatementResult{}, err
	}
	maxSQLResultRows := GetMySQLQueryResultLimit()
	maxSQLResultBytes := GetMySQLQueryResultMaxBytes()
	resultBytes := 0
	result := MySQLStatementResult{Columns: columns, Rows: make([][]any, 0, maxSQLResultRows)}
	for rows.Next() {
		if len(result.Rows) >= maxSQLResultRows {
			result.Truncated = true
			break
		}
		values := make([]any, len(columns))
		refs := make([]any, len(columns))
		for i := range values {
			refs[i] = &values[i]
		}
		if err := rows.Scan(refs...); err != nil {
			return MySQLStatementResult{}, err
		}
		rowBytes := 0
		for i, value := range values {
			values[i] = mysqlJSONValue(value)
			rowBytes += len(fmt.Sprint(values[i]))
		}
		if resultBytes+rowBytes > maxSQLResultBytes {
			result.Truncated = true
			break
		}
		result.Rows = append(result.Rows, values)
		resultBytes += rowBytes
	}
	return result, rows.Err()
}

// redactSQLForHistory retains structure useful for auditing without retaining literal values.
func redactSQLForHistory(statement string) string {
	var result strings.Builder
	quote := rune(0)
	escaped := false
	for _, character := range strings.TrimSpace(statement) {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			result.WriteByte('?')
			continue
		}
		result.WriteRune(character)
	}
	value := result.String()
	for _, keyword := range []string{"PASSWORD", "TOKEN", "SECRET", "AUTHORIZATION"} {
		re := regexp.MustCompile(`(?i)(` + keyword + `\s*(?:=|TO)?\s*)([^,;\s]+)`)
		value = re.ReplaceAllString(value, "${1}***")
	}
	if len(value) > 4096 {
		return value[:4096] + "..."
	}
	return value
}

// mysqlJSONValue keeps arbitrary MySQL result cells safe for JSON responses.
func mysqlJSONValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return fmt.Sprint(typed)
		}
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return fmt.Sprint(typed)
		}
	}
	return value
}

func recordMySQLSQLHistory(sourceID, database, statement, username string, result MySQLStatementResult, executionErr error, duration time.Duration) {
	current := currentStore()
	if current == nil || sourceID == "" || statement == "" {
		return
	}
	status := "success"
	errorMessage := ""
	if executionErr != nil {
		status = "failed"
		errorMessage = executionErr.Error()
		if len(errorMessage) > 1000 {
			errorMessage = errorMessage[:1000]
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = current.ExecContext(ctx, `INSERT INTO mysql_sql_history
		(source_id, database_name, statement_text, status, result_rows, affected_rows, duration_ms, error_message, username)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, sourceID, database, statement, status, len(result.Rows), result.Affected, duration.Milliseconds(), errorMessage, username)
}

func ListMySQLSQLHistory(sourceID string, page, pageSize int) ([]MySQLSQLHistory, int) {
	current := currentStore()
	if current == nil || strings.TrimSpace(sourceID) == "" {
		return []MySQLSQLHistory{}, 0
	}
	if page < 1 { page = 1 }
	if pageSize <= 0 || pageSize > 100 { pageSize = 10 }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var total int
	if err := current.QueryRowContext(ctx, `SELECT COUNT(*) FROM mysql_sql_history WHERE source_id = ?`, sourceID).Scan(&total); err != nil { return []MySQLSQLHistory{}, 0 }
	offset := (page - 1) * pageSize
	rows, err := current.QueryContext(ctx, `SELECT id, source_id, database_name, statement_text, status,
		result_rows, affected_rows, duration_ms, error_message, username, created_at
		FROM mysql_sql_history WHERE source_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`, sourceID, pageSize, offset)
	if err != nil {
		return []MySQLSQLHistory{}, total
	}
	defer rows.Close()
	items := make([]MySQLSQLHistory, 0, pageSize)
	for rows.Next() {
		var item MySQLSQLHistory
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.SourceID, &item.Database, &item.Statement, &item.Status, &item.ResultRows, &item.Affected, &item.DurationMS, &item.Error, &item.Username, &createdAt); err != nil {
			continue
		}
		item.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
		items = append(items, item)
	}
	return items, total
}

func mysqlStatementKeyword(statement string) string {
	depth := 0
	quote := byte(0)
	withStatement := false
	for index := 0; index < len(statement); {
		char := statement[index]
		if quote != 0 {
			if char == '\\' && index+1 < len(statement) {
				index += 2
				continue
			}
			if char == quote {
				quote = 0
			}
			index++
			continue
		}
		if char == '\'' || char == '"' || char == '`' {
			quote = char
			index++
			continue
		}
		if char == '#' {
			for index < len(statement) && statement[index] != '\n' {
				index++
			}
			continue
		}
		if char == '-' && index+1 < len(statement) && statement[index+1] == '-' {
			for index < len(statement) && statement[index] != '\n' {
				index++
			}
			continue
		}
		if char == '/' && index+1 < len(statement) && statement[index+1] == '*' {
			index += 2
			for index+1 < len(statement) && !(statement[index] == '*' && statement[index+1] == '/') {
				index++
			}
			if index+1 < len(statement) {
				index += 2
			}
			continue
		}
		if char == '(' {
			depth++
			index++
			continue
		}
		if char == ')' {
			if depth > 0 {
				depth--
			}
			index++
			continue
		}
		if depth == 0 && ((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')) {
			start := index
			for index < len(statement) {
				current := statement[index]
				if !((current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z') || current == '_') {
					break
				}
				index++
			}
			word := strings.ToUpper(statement[start:index])
			if !withStatement {
				if word != "WITH" {
					return word
				}
				withStatement = true
				continue
			}
			switch word {
			case "SELECT", "INSERT", "UPDATE", "DELETE", "REPLACE":
				return word
			}
			continue
		}
		index++
	}
	return ""
}

func PreviewMySQLTable(sourceID, database, table string, page, pageSize int) (MySQLTablePreview, error) {
	return PreviewMySQLTableWithOptions(sourceID, database, table, page, pageSize, MySQLTablePreviewOptions{})
}

func PreviewMySQLTableWithOptions(sourceID, database, table string, page, pageSize int, options MySQLTablePreviewOptions) (MySQLTablePreview, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(ds.Type, "mysql") || !ds.Enabled {
		return MySQLTablePreview{}, errors.New("MySQL data source not available")
	}
	if !validSQLIdentifier(database) || !validSQLIdentifier(table) {
		return MySQLTablePreview{}, errors.New("invalid database or table name")
	}
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return MySQLTablePreview{}, err
	}
	defer db.Close()
	metadataCtx, cancelMetadata := context.WithTimeout(context.Background(), 3*time.Second)
	columns, err := mysqlTableColumnNames(metadataCtx, db, database, table)
	cancelMetadata()
	if err != nil {
		return MySQLTablePreview{}, err
	}
	filterColumn := strings.TrimSpace(options.FilterColumn)
	filterValue := strings.TrimSpace(options.FilterValue)
	sortColumn := strings.TrimSpace(options.SortColumn)
	allowedColumns := make(map[string]bool, len(columns))
	for _, column := range columns {
		allowedColumns[column] = true
	}
	if filterColumn != "" && !allowedColumns[filterColumn] {
		return MySQLTablePreview{}, errors.New("invalid filter column")
	}
	if sortColumn != "" && !allowedColumns[sortColumn] {
		return MySQLTablePreview{}, errors.New("invalid sort column")
	}
	whereClause := ""
	whereArgs := make([]any, 0, 1)
	if filterColumn != "" && filterValue != "" {
		whereClause = fmt.Sprintf(" WHERE LOCATE(?, CAST(`%s` AS CHAR)) > 0", filterColumn)
		whereArgs = append(whereArgs, filterValue)
	}
	var total int64
	totalExact := true
	if whereClause == "" {
		total, totalExact = mysqlTableRowCount(sourceID, db, database, table)
	} else {
		countCtx, cancelCount := context.WithTimeout(context.Background(), 5*time.Second)
		err = db.QueryRowContext(countCtx, fmt.Sprintf("SELECT COUNT(*) FROM `%s`.`%s`%s", database, table, whereClause), whereArgs...).Scan(&total)
		cancelCount()
		if err != nil {
			return MySQLTablePreview{}, fmt.Errorf("filter count failed: %w", err)
		}
	}
	requestedPage := page
	page, pageSize, totalPages := normalizeMySQLPagination(page, pageSize, total)
	if !totalExact {
		page = max(1, requestedPage)
		totalPages = max(totalPages, page)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	orderBy := ""
	if sortColumn != "" {
		direction := "ASC"
		if strings.EqualFold(strings.TrimSpace(options.SortDirection), "desc") {
			direction = "DESC"
		}
		orderBy = fmt.Sprintf(" ORDER BY `%s` %s", sortColumn, direction)
	} else {
		orderBy = mysqlPreviewOrderBy(ctx, db, database, table)
	}
	offset := (page - 1) * pageSize
	queryArgs := append([]any{}, whereArgs...)
	queryArgs = append(queryArgs, pageSize, offset)
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM `%s`.`%s`%s%s LIMIT ? OFFSET ?", database, table, whereClause, orderBy), queryArgs...)
	if err != nil {
		return MySQLTablePreview{}, err
	}
	defer rows.Close()
	resultColumns, err := rows.Columns()
	if err != nil {
		return MySQLTablePreview{}, err
	}
	result := MySQLTablePreview{Columns: resultColumns, Rows: make([][]any, 0), Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages, TotalExact: totalExact}
	for rows.Next() {
		values := make([]any, len(resultColumns))
		pointers := make([]any, len(resultColumns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return MySQLTablePreview{}, err
		}
		for i, value := range values {
			if raw, ok := value.([]byte); ok {
				values[i] = string(raw)
			}
			if value == nil {
				values[i] = nil
			}
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return MySQLTablePreview{}, err
	}
	if !totalExact {
		observed := int64(offset + len(result.Rows))
		if observed > result.Total {
			result.Total = observed
		}
		if len(result.Rows) == pageSize && result.Total <= observed {
			result.Total = observed + 1
		}
		result.TotalPages = max(page, int((result.Total+int64(pageSize)-1)/int64(pageSize)))
	}
	return result, nil
}

func mysqlTableColumnNames(ctx context.Context, db *sql.DB, database, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`, database, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make([]string, 0)
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		if !validSQLIdentifier(column) {
			return nil, errors.New("invalid table column")
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, errors.New("table has no columns")
	}
	return columns, nil
}

func mysqlTableRowCount(sourceID string, db *sql.DB, database, table string) (int64, bool) {
	cacheKey := sourceID + "\x00" + database + "\x00" + table
	if cached, ok := mysqlTableCountCache.Load(cacheKey); ok {
		entry := cached.(mysqlTableCountCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.total, entry.exact
		}
		mysqlTableCountCache.Delete(cacheKey)
	}
	var estimate sql.NullInt64
	estimateCtx, cancelEstimate := context.WithTimeout(context.Background(), 2*time.Second)
	err := db.QueryRowContext(estimateCtx, `SELECT table_rows FROM information_schema.tables WHERE table_schema = ? AND table_name = ?`, database, table).Scan(&estimate)
	cancelEstimate()
	estimatedTotal := int64(0)
	if err == nil && estimate.Valid && estimate.Int64 > 0 {
		estimatedTotal = estimate.Int64
	}
	if estimatedTotal >= 100000 {
		mysqlTableCountCache.Store(cacheKey, mysqlTableCountCacheEntry{total: estimatedTotal, exact: false, expiresAt: time.Now().Add(30 * time.Second)})
		return estimatedTotal, false
	}
	var exactTotal int64
	countCtx, cancelCount := context.WithTimeout(context.Background(), 5*time.Second)
	err = db.QueryRowContext(countCtx, fmt.Sprintf("SELECT COUNT(*) FROM `%s`.`%s`", database, table)).Scan(&exactTotal)
	cancelCount()
	if err == nil {
		mysqlTableCountCache.Store(cacheKey, mysqlTableCountCacheEntry{total: exactTotal, exact: true, expiresAt: time.Now().Add(10 * time.Second)})
		return exactTotal, true
	}
	mysqlTableCountCache.Store(cacheKey, mysqlTableCountCacheEntry{total: estimatedTotal, exact: false, expiresAt: time.Now().Add(15 * time.Second)})
	return estimatedTotal, false
}

func clearMySQLTableCountCache() {
	mysqlTableCountCache.Range(func(key, _ any) bool {
		mysqlTableCountCache.Delete(key)
		return true
	})
}

func normalizeMySQLPagination(page, pageSize int, total int64) (int, int, int) {
	switch pageSize {
	case 50, 100, 200, 500:
	default:
		pageSize = 50
	}
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	return page, pageSize, totalPages
}

func mysqlPreviewOrderBy(ctx context.Context, db *sql.DB, database, table string) string {
	rows, err := db.QueryContext(ctx, `SELECT column_name FROM information_schema.statistics
		WHERE table_schema = ? AND table_name = ? AND index_name = 'PRIMARY' ORDER BY seq_in_index`, database, table)
	if err != nil {
		return ""
	}
	defer rows.Close()
	columns := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil || !validSQLIdentifier(name) {
			return ""
		}
		columns = append(columns, "`"+name+"`")
	}
	if len(columns) == 0 {
		return ""
	}
	return " ORDER BY " + strings.Join(columns, ", ")
}

func DescribeMySQLTable(sourceID, database, table string) ([]MySQLColumnDetail, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(ds.Type, "mysql") || !ds.Enabled {
		return nil, errors.New("MySQL data source not available")
	}
	if !validSQLIdentifier(database) || !validSQLIdentifier(table) {
		return nil, errors.New("invalid database or table name")
	}
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SHOW FULL COLUMNS FROM `%s`.`%s`", database, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]MySQLColumnDetail, 0)
	for rows.Next() {
		var item MySQLColumnDetail
		var collation, defaultValue sql.NullString
		var nullable string
		if err := rows.Scan(&item.Name, &item.Type, &collation, &nullable, &item.Key, &defaultValue, &item.Extra, &item.Privileges, &item.Comment); err != nil {
			return nil, err
		}
		item.Nullable = strings.EqualFold(nullable, "YES")
		if collation.Valid {
			item.Collation = &collation.String
		}
		if defaultValue.Valid {
			item.Default = &defaultValue.String
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func InsertMySQLTableRow(sourceID, database, table string, values map[string]any) (MySQLMutationResult, error) {
	columns, _, err := editableMySQLColumns(sourceID, database, table)
	if err != nil {
		return MySQLMutationResult{}, err
	}
	names, args, err := validatedMySQLValues(values, columns)
	if err != nil {
		return MySQLMutationResult{}, err
	}
	quoted := make([]string, len(names))
	placeholders := make([]string, len(names))
	for index, name := range names {
		quoted[index] = "`" + name + "`"
		placeholders[index] = "?"
	}
	query := fmt.Sprintf("INSERT INTO `%s`.`%s` (%s) VALUES (%s)", database, table, strings.Join(quoted, ", "), strings.Join(placeholders, ", "))
	return executeMySQLMutation(sourceID, query, args)
}

func UpdateMySQLTableRow(sourceID, database, table string, keys, values map[string]any) (MySQLMutationResult, error) {
	columns, primaryKeys, err := editableMySQLColumns(sourceID, database, table)
	if err != nil {
		return MySQLMutationResult{}, err
	}
	if err := validateMySQLRowKeys(keys, primaryKeys); err != nil {
		return MySQLMutationResult{}, err
	}
	names, args, err := validatedMySQLValues(values, columns)
	if err != nil {
		return MySQLMutationResult{}, err
	}
	assignments := make([]string, len(names))
	for index, name := range names {
		assignments[index] = "`" + name + "` = ?"
	}
	where, keyArgs := mysqlPrimaryKeyWhere(primaryKeys, keys)
	args = append(args, keyArgs...)
	query := fmt.Sprintf("UPDATE `%s`.`%s` SET %s WHERE %s", database, table, strings.Join(assignments, ", "), where)
	return executeMySQLMutation(sourceID, query, args)
}

func DeleteMySQLTableRow(sourceID, database, table string, keys map[string]any) (MySQLMutationResult, error) {
	_, primaryKeys, err := editableMySQLColumns(sourceID, database, table)
	if err != nil {
		return MySQLMutationResult{}, err
	}
	if err := validateMySQLRowKeys(keys, primaryKeys); err != nil {
		return MySQLMutationResult{}, err
	}
	where, args := mysqlPrimaryKeyWhere(primaryKeys, keys)
	query := fmt.Sprintf("DELETE FROM `%s`.`%s` WHERE %s", database, table, where)
	return executeMySQLMutation(sourceID, query, args)
}

func editableMySQLColumns(sourceID, database, table string) (map[string]MySQLColumnDetail, []string, error) {
	if isMySQLSystemDatabase(database) {
		return nil, nil, errors.New("系统数据库不允许编辑")
	}
	details, err := DescribeMySQLTable(sourceID, database, table)
	if err != nil {
		return nil, nil, err
	}
	columns := make(map[string]MySQLColumnDetail, len(details))
	primaryKeys := make([]string, 0)
	for _, item := range details {
		columns[item.Name] = item
		if strings.EqualFold(item.Key, "PRI") {
			primaryKeys = append(primaryKeys, item.Name)
		}
	}
	return columns, primaryKeys, nil
}

func validatedMySQLValues(values map[string]any, columns map[string]MySQLColumnDetail) ([]string, []any, error) {
	if len(values) == 0 {
		return nil, nil, errors.New("没有需要保存的字段")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		if _, ok := columns[name]; !ok {
			return nil, nil, fmt.Errorf("字段不存在：%s", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	args := make([]any, len(names))
	for index, name := range names {
		args[index] = values[name]
	}
	return names, args, nil
}

func validateMySQLRowKeys(keys map[string]any, primaryKeys []string) error {
	if len(primaryKeys) == 0 {
		return errors.New("该表没有主键，不能安全更新或删除记录")
	}
	if len(keys) != len(primaryKeys) {
		return errors.New("主键字段不完整")
	}
	for _, name := range primaryKeys {
		if value, ok := keys[name]; !ok || value == nil {
			return fmt.Errorf("主键字段无效：%s", name)
		}
	}
	return nil
}

func mysqlPrimaryKeyWhere(primaryKeys []string, keys map[string]any) (string, []any) {
	conditions := make([]string, len(primaryKeys))
	args := make([]any, len(primaryKeys))
	for index, name := range primaryKeys {
		conditions[index] = "`" + name + "` <=> ?"
		args[index] = keys[name]
	}
	return strings.Join(conditions, " AND "), args
}

func executeMySQLMutation(sourceID, query string, args []any) (MySQLMutationResult, error) {
	ds, err := GetDataSourceByID(sourceID)
	if err != nil || !strings.EqualFold(ds.Type, "mysql") || !ds.Enabled {
		return MySQLMutationResult{}, errors.New("MySQL data source not available")
	}
	db, err := openMySQLDataSource(ds)
	if err != nil {
		return MySQLMutationResult{}, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return MySQLMutationResult{}, err
	}
	affected, _ := result.RowsAffected()
	clearMySQLTableCountCache()
	return MySQLMutationResult{Affected: affected}, nil
}

func isMySQLSystemDatabase(database string) bool {
	switch strings.ToLower(strings.TrimSpace(database)) {
	case "information_schema", "mysql", "performance_schema", "sys":
		return true
	default:
		return false
	}
}
