package service

import "monitor-platform/internal/model"

func GetOverviewMetrics() []model.OverviewMetric {
	return []model.OverviewMetric{}
}

func GetAlerts() []model.AlertItem {
	return []model.AlertItem{}
}

func GetInspectionTasks() []model.InspectionTask {
	return []model.InspectionTask{}
}

func GetSystemConfig() model.SystemConfig {
	return model.SystemConfig{
		PlatformName:   GetPlatformName(),
		RequiredFields: []string{"平台名称", "部署环境", "责任人", "告警邮箱"},
		OptionalFields: []string{"短信接收人", "SMTP地址", "OpenTelemetry地址", "告警Webhook"},
	}
}
