package service

import (
	"strings"
	"testing"

	"monitor-platform/internal/model"
)

func TestValidateCollectionRule(t *testing.T) {
	tests := []struct {
		name      string
		rule      model.CollectionRule
		wantError string
	}{
		{
			name: "valid mysql rule",
			rule: model.CollectionRule{Name: "daily orders", Source: "mysql-1", Database: "orders", Table: "payments", Field: "created_at", Condition: "当天有数据"},
		},
		{
			name: "valid http rule",
			rule: model.CollectionRule{Name: "health", Source: "custom-probe", Database: "http", Table: "https://example.com/health", Condition: "状态码小于400"},
		},
		{
			name: "valid tcp rule",
			rule: model.CollectionRule{Name: "mysql port", Source: "custom-probe", Database: "tcp", Table: "127.0.0.1:3306", Condition: "TCP端口可连接"},
		},
		{
			name: "valid prometheus rule",
			rule: model.CollectionRule{Name: "instance down", Source: "prom-1", Database: "prometheus", Table: "up", Condition: "小于", Threshold: "1"},
		},
		{
			name: "valid file monitor rule",
			rule: model.CollectionRule{Name: "daily report", Source: "ssh-1", Database: "file-monitor", Table: "/var/reports", Field: `^report_\d{8}\.csv$`, Condition: "当天生成文件", TimeWindow: "09:00"},
		},
		{
			name: "valid multi-node script monitor rule",
			rule: model.CollectionRule{Name: "disk check", Source: "ssh-1,ssh-2", Database: "script-monitor", Table: "test -d /data", Condition: "退出码等于", Threshold: "0"},
		},
		{
			name:      "script monitor requires valid exit code",
			rule:      model.CollectionRule{Name: "disk check", Source: "ssh-1", Database: "script-monitor", Table: "test -d /data", Condition: "退出码等于", Threshold: "300"},
			wantError: "预期退出码必须是 0 到 255",
		},
		{
			name:      "name is required",
			rule:      model.CollectionRule{Source: "custom-probe", Database: "tcp", Table: "127.0.0.1:3306"},
			wantError: "规则名称不能为空",
		},
		{
			name:      "data monitor date field is required",
			rule:      model.CollectionRule{Name: "daily orders", Source: "mysql-1", Database: "orders", Condition: "当天有数据"},
			wantError: "数据监控规则必须选择数据源、数据库、表和日期字段",
		},
		{
			name:      "http condition is supported",
			rule:      model.CollectionRule{Name: "health", Source: "custom-probe", Database: "http", Table: "https://example.com", Condition: "大于"},
			wantError: "HTTP 探测判断方式不支持",
		},
		{
			name:      "http expected status is required",
			rule:      model.CollectionRule{Name: "health", Source: "custom-probe", Database: "http", Table: "https://example.com", Condition: "状态码等于"},
			wantError: "HTTP 状态码必须是 100 到 599 之间的数字",
		},
		{
			name:      "tcp target has port",
			rule:      model.CollectionRule{Name: "mysql port", Source: "custom-probe", Database: "tcp", Table: "127.0.0.1", Condition: "TCP端口可连接"},
			wantError: "TCP 探测目标必须是 host:port",
		},
		{
			name:      "prometheus threshold is numeric",
			rule:      model.CollectionRule{Name: "instance down", Source: "prom-1", Database: "prometheus", Table: "up", Condition: "小于", Threshold: "not-a-number"},
			wantError: "Prometheus 规则阈值必须是数字",
		},
		{
			name:      "file monitor requires absolute path",
			rule:      model.CollectionRule{Name: "daily report", Source: "ssh-1", Database: "file-monitor", Table: "reports", Field: `.*`, Condition: "当天生成文件", TimeWindow: "09:00"},
			wantError: "文件检测路径必须是有效的 Linux 绝对路径",
		},
		{
			name:      "file monitor requires valid regex",
			rule:      model.CollectionRule{Name: "daily report", Source: "ssh-1", Database: "file-monitor", Table: "/var/reports", Field: "[", Condition: "当天生成文件", TimeWindow: "09:00"},
			wantError: "文件名正则无效",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCollectionRule(test.rule)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("validateCollectionRule() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateCollectionRule() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestNormalizeCollectionRuleMigratesLegacyTodayCondition(t *testing.T) {
	rule := normalizeCollectionRule(model.CollectionRule{Name: "daily orders", Condition: "今天有数据"})
	if rule.Condition != "当天有数据" {
		t.Fatalf("normalizeCollectionRule() condition = %q", rule.Condition)
	}
}
