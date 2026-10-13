package service

import "testing"

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
