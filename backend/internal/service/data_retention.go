package service

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

func startDataRetentionJob() {
	runDataRetention()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			runDataRetention()
		}
	}()
}

func runDataRetention() {
	current := currentStore()
	metricsMu.RLock()
	metrics := metricsDB
	metricsMu.RUnlock()
	if current == nil || metrics == nil {
		return
	}
	for _, target := range []struct{ table, interval string }{
		{"ssh_metric_samples", "7 DAY"},
		{"host_filesystem_samples", "7 DAY"},
		{"mysql_metric_samples", "7 DAY"},
		{"middleware_metric_samples", "7 DAY"},
		{"audit_logs", "180 DAY"},
		{"mysql_sql_history", "180 DAY"},
		{"mysql_async_jobs", "30 DAY"},
	} {
		targetDB, column := metrics, "collected_at"
		if target.table == "audit_logs" || target.table == "mysql_sql_history" || target.table == "mysql_async_jobs" {
			targetDB, column = current, "created_at"
		}
		if _, err := targetDB.Exec("DELETE FROM " + target.table + " WHERE " + column + " < NOW() - INTERVAL " + target.interval); err != nil {
			log.Printf("data retention %s: %v", target.table, err)
		}
	}
	// Remove abandoned upload files left by failed requests or process exits.
	if entries, err := os.ReadDir(filepath.Join(os.TempDir(), "opsguard-mysql-jobs")); err == nil {
		cutoff := time.Now().Add(-7 * 24 * time.Hour)
		for _, entry := range entries {
			if info, statErr := entry.Info(); statErr == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(os.TempDir(), "opsguard-mysql-jobs", entry.Name()))
			}
		}
	}
}
