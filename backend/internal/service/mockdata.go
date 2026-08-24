package service

import (
	"time"

	"monitor-platform/internal/model"
)

func GetOverviewMetrics() []model.OverviewMetric {
	return []model.OverviewMetric{
		{Label: "全网请求数", Value: "812.4K", Trend: "+12.8%", Unit: "次/分钟"},
		{Label: "应用可用率", Value: "99.97%", Trend: "+0.03%", Unit: "SLA"},
		{Label: "平均响应时间", Value: "184ms", Trend: "-9.4%", Unit: "ms"},
		{Label: "异常告警", Value: "7", Trend: "-2", Unit: "条"},
	}
}

func GetAlerts() []model.AlertItem {
	return []model.AlertItem{
		{Title: "MySQL慢查询增多", Severity: "warning", Message: "订单库 query_time 超过 2s 的 SQL 占比达到 12.1%", Timestamp: "2分钟前"},
		{Title: "Kafka消费延迟", Severity: "error", Message: "消息消费最大堆积 3.8 分钟，已触发降级策略", Timestamp: "8分钟前"},
		{Title: "Redis连接异常", Severity: "info", Message: "缓存集群节点 1 发生短暂断连，已自动切回读写分离", Timestamp: "21分钟前"},
	}
}

func GetInspectionTasks() []model.InspectionTask {
	return []model.InspectionTask{
		{ID: "insp-101", Title: "订单系统巡检", Owner: "刘旭", Status: "运行中", Progress: 84, LastUpdated: time.Now().Add(-10 * time.Minute)},
		{ID: "insp-102", Title: "支付链路巡检", Owner: "周琳", Status: "待执行", Progress: 24, LastUpdated: time.Now().Add(-32 * time.Minute)},
		{ID: "insp-103", Title: "日志采集健康检查", Owner: "许凯", Status: "已完成", Progress: 100, LastUpdated: time.Now().Add(-2 * time.Hour)},
	}
}

func GetDataSources() []model.DataSource {
	return []model.DataSource{
		{ID: "mysql-01", Name: "订单主库", Type: "MySQL", Host: "10.10.20.18", Port: "3306", Username: "monitor", Password: "********", Database: "order_center", Description: "订单、支付大表实时采集", Status: "健康", LastTest: "2分钟前", Tags: []string{"主库", "读写"}},
		{ID: "kafka-01", Name: "日志消息总线", Type: "Kafka", Host: "10.10.20.31", Port: "9092", Username: "producer", Password: "********", Database: "platform_log", Description: "业务日志、告警事件流", Status: "健康", LastTest: "1分钟前", Tags: []string{"消息队列", "实时"}},
		{ID: "redis-01", Name: "缓存集群", Type: "Redis", Host: "10.10.20.45", Port: "6379", Username: "cache-admin", Password: "********", Database: "0", Description: "热点缓存、限流、会话存储", Status: "健康", LastTest: "5分钟前", Tags: []string{"缓存", "高可用"}},
		{ID: "elasticsearch-01", Name: "搜索索引集群", Type: "Elasticsearch", Host: "10.10.20.56", Port: "9200", Username: "es-reader", Password: "********", Database: "incident-index", Description: "日志搜索与事件溯源", Status: "待配置", LastTest: "未测试", Tags: []string{"检索", "分析"}},
	}
}

func GetSystemConfig() model.SystemConfig {
	return model.SystemConfig{
		PlatformName:     "数据平台监控中台",
		Environment:      "生产环境",
		Responsible:      "平台运维部",
		NotificationMail: "ops@company.com",
		SMSReceiver:      "13800000001",
		SMTPHost:         "smtp.company.com",
		SMTPPort:         "465",
		OpenTelemetry:    "http://10.10.20.4:4318",
		AlertWebhook:     "https://hooks.company.com/ops-monitor",
		RequiredFields:   []string{"平台名称", "部署环境", "责任人", "告警邮箱"},
		OptionalFields:   []string{"短信接收人", "SMTP地址", "OpenTelemetry地址", "告警Webhook"},
	}
}

func GetCollectionRules() []model.CollectionRule {
	return []model.CollectionRule{
		{ID: "rule-001", Name: "订单支付慢查询", Source: "MySQL", Database: "order_center", Table: "payment_orders", Field: "paid_at", Condition: "今天有数据", Threshold: "", TimeWindow: "24h", LastRun: "刚刚", Status: "启用"},
		{ID: "rule-002", Name: "库存预警值为 0", Source: "Redis", Database: "inventory", Table: "stock_info", Field: "available_qty", Condition: "数值为 0", Threshold: "0", TimeWindow: "1h", LastRun: "12分钟前", Status: "启用"},
		{ID: "rule-003", Name: "订单状态为空", Source: "MySQL", Database: "order_center", Table: "orders", Field: "status", Condition: "为空", Threshold: "", TimeWindow: "6h", LastRun: "31分钟前", Status: "待确认"},
	}
}
