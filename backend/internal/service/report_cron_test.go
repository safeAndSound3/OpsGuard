package service

import (
	"strings"
	"testing"
	"time"

	"monitor-platform/internal/model"
)

func TestReportCron(t *testing.T) {
	for _, s := range []string{"0 */30 * * * ?", "30 0 8 1 * ?", "0 0 8 */3 * ?", "0 0 8 ? * 2", "0 0/15 8-18 * * ?"} {
		if _, e := parseReportCron(s); e != nil {
			t.Errorf("%s: %v", s, e)
		}
	}
	for _, s := range []string{"0 */0 * * * ?", "0 60 * * * ?", "0 0 8 1* * ?", "0 0 8 4-1 * ?", "0 0 8 * * *", "? 0 8 * * ?", "0 0 8 ? * ?"} {
		if _, e := parseReportCron(s); e == nil {
			t.Errorf("accepted invalid %s", s)
		}
	}
	c, _ := parseReportCron("0 */30 * * * ?")
	for _, tc := range []struct {
		minute int
		want   bool
	}{{0, true}, {15, false}, {30, true}, {59, false}} {
		if c.matches(time.Date(2026, 9, 24, 8, tc.minute, 0, 0, time.Local)) != tc.want {
			t.Errorf("minute %d", tc.minute)
		}
	}
	c, _ = parseReportCron("0 0 8 */3 * ?")
	if !c.matches(time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local)) || c.matches(time.Date(2026, 10, 2, 8, 0, 0, 0, time.Local)) {
		t.Fatal("day step must restart monthly")
	}
	c, _ = parseReportCron("0 0 8 ? * 2")
	if !c.matches(time.Date(2026, 9, 28, 8, 0, 0, 0, time.Local)) {
		t.Fatal("weekday 2 must be Monday")
	}
}

func TestReportTimeWindow(t *testing.T) {
	now := time.Date(2026, 9, 24, 15, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	start, end, err := reportTimeWindow("昨天", now)
	if err != nil || start.Format("2006-01-02") != "2026-09-23" || end.Format("2006-01-02") != "2026-09-24" {
		t.Fatalf("yesterday window: %v %v %v", start, end, err)
	}
	query, err := renderReportSQL("SELECT * FROM logs WHERE created_at >= '${start}' AND created_at < '${end}'", "本月", now)
	if err != nil || !strings.Contains(query, "2026-09-01 00:00:00") || !strings.Contains(query, "2026-10-01 00:00:00") {
		t.Fatalf("rendered query: %s (%v)", query, err)
	}
	if _, err := renderReportSQL("SELECT '${start}'", "", now); err == nil {
		t.Fatal("expected missing time range error")
	}
}

func TestReportDueTimeCatchUp(t *testing.T) {
	cron, _ := parseReportCron("0 0 * * * ?")
	last := time.Date(2026, 9, 24, 7, 0, 0, 0, time.Local).Format(time.RFC3339)
	task := model.MySQLReportTask{LastRunAt: &last}
	due, ok := reportDueTime(task, cron, time.Date(2026, 9, 24, 9, 1, 0, 0, time.Local))
	if !ok || due.Hour() != 9 {
		t.Fatalf("expected missed 09:00 run, got %v", due)
	}
}

func TestReportReadOnlySQL(t *testing.T) {
	for _, s := range []string{"SELECT 1", "WITH t AS (SELECT 1 n) SELECT n FROM t", "SELECT\n1"} {
		if !isReadOnlyReportSQL(s) {
			t.Errorf("rejected %s", s)
		}
	}
	for _, s := range []string{"WITH t AS (SELECT 1) DELETE FROM users", "SELECT 1; DELETE FROM users", "SELECT 1 INTO OUTFILE '/tmp/a'", "/*!50000 DELETE FROM users */", "EXPLAIN ANALYZE DELETE FROM users"} {
		if isReadOnlyReportSQL(s) {
			t.Errorf("unsafe SQL accepted: %s", s)
		}
	}
}

func TestReportDataSourceTypes(t *testing.T) {
	for _, sourceType := range []string{"MySQL", "PostgreSQL", "Postgres", "Hive"} {
		if !isReportDataSourceType(sourceType) {
			t.Errorf("rejected report data source type %s", sourceType)
		}
	}
	for _, sourceType := range []string{"", "Redis", "Prometheus"} {
		if isReportDataSourceType(sourceType) {
			t.Errorf("accepted unsupported report data source type %s", sourceType)
		}
	}
}
