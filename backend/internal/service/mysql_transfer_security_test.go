package service

import (
	"strings"
	"testing"
)

func TestRedactSQLForHistoryRedactsQuotedAndBareSecrets(t *testing.T) {
	value := redactSQLForHistory("SET PASSWORD = raw-secret; INSERT INTO users(token) VALUES ('secret-value')")
	for _, secret := range []string{"raw-secret", "secret-value"} {
		if strings.Contains(value, secret) {
			t.Fatalf("redacted SQL still contains %q: %s", secret, value)
		}
	}
}

func TestSplitMySQLStatementsSupportsDelimiterAndComments(t *testing.T) {
	content := "-- preamble;\nDELIMITER $$\nCREATE PROCEDURE p()\nBEGIN\n  SELECT 'one;two';\nEND$$\nDELIMITER ;\n/* keep ; */\nINSERT INTO t VALUES (1);"
	statements, err := splitMySQLStatements(content)
	if err != nil {
		t.Fatalf("splitMySQLStatements() error = %v", err)
	}
	if len(statements) != 2 {
		t.Fatalf("splitMySQLStatements() returned %d statements: %#v", len(statements), statements)
	}
	if !strings.Contains(statements[0], "SELECT 'one;two'") || !strings.HasPrefix(statements[1], "/* keep ; */") {
		t.Fatalf("split statements = %#v", statements)
	}
}
