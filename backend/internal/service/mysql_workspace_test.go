package service

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidatedMySQLValuesUsesColumnWhitelistAndStableOrder(t *testing.T) {
	columns := map[string]MySQLColumnDetail{
		"id":   {Name: "id"},
		"name": {Name: "name"},
	}
	names, args, err := validatedMySQLValues(map[string]any{"name": "OpsGuard", "id": 7}, columns)
	if err != nil {
		t.Fatalf("validatedMySQLValues() error = %v", err)
	}
	if !reflect.DeepEqual(names, []string{"id", "name"}) || !reflect.DeepEqual(args, []any{7, "OpsGuard"}) {
		t.Fatalf("validatedMySQLValues() = %v, %v", names, args)
	}
	if _, _, err := validatedMySQLValues(map[string]any{"unknown": "value"}, columns); err == nil || !strings.Contains(err.Error(), "字段不存在") {
		t.Fatalf("unknown column error = %v", err)
	}
}

func TestValidateMySQLRowKeysRequiresCompletePrimaryKey(t *testing.T) {
	if err := validateMySQLRowKeys(map[string]any{"tenant_id": 1}, []string{"tenant_id", "id"}); err == nil {
		t.Fatal("incomplete composite key should fail")
	}
	if err := validateMySQLRowKeys(map[string]any{"tenant_id": 1, "id": 2}, []string{"tenant_id", "id"}); err != nil {
		t.Fatalf("complete composite key error = %v", err)
	}
	if err := validateMySQLRowKeys(map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "没有主键") {
		t.Fatalf("missing primary key error = %v", err)
	}
}

func TestMySQLSystemDatabasesAreNotEditable(t *testing.T) {
	for _, database := range []string{"mysql", "information_schema", "performance_schema", "sys"} {
		if !isMySQLSystemDatabase(database) {
			t.Fatalf("%s should be protected", database)
		}
	}
	if isMySQLSystemDatabase("opsguard") {
		t.Fatal("application database should be editable")
	}
}

func TestMySQLStatementKeywordHandlesCommentsAndCTEs(t *testing.T) {
	tests := map[string]string{
		"-- inspect\nSELECT 1": "SELECT",
		"WITH recent AS (SELECT id FROM jobs) SELECT * FROM recent": "SELECT",
		"WITH recent AS (SELECT id FROM jobs) UPDATE jobs SET id=1": "UPDATE",
		"/* note */ DELETE FROM jobs WHERE id = 1":                  "DELETE",
	}
	for statement, want := range tests {
		if got := mysqlStatementKeyword(statement); got != want {
			t.Errorf("mysqlStatementKeyword(%q) = %q, want %q", statement, got, want)
		}
	}
}

func TestNormalizeMySQLPagination(t *testing.T) {
	page, pageSize, totalPages := normalizeMySQLPagination(99, 100, 201)
	if page != 3 || pageSize != 100 || totalPages != 3 {
		t.Fatalf("normalizeMySQLPagination() = %d, %d, %d", page, pageSize, totalPages)
	}
	page, pageSize, totalPages = normalizeMySQLPagination(0, 42, 0)
	if page != 1 || pageSize != 50 || totalPages != 1 {
		t.Fatalf("normalizeMySQLPagination() defaults = %d, %d, %d", page, pageSize, totalPages)
	}
}
