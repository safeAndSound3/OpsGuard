package model

import "time"

type DataSource struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Host        string   `json:"host"`
	Port        string   `json:"port"`
	Username    string   `json:"username,omitempty"`
	Password    string   `json:"password,omitempty"`
	Database    string   `json:"database,omitempty"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	LastTest    string   `json:"lastTest"`
	Tags        []string `json:"tags,omitempty"`
}

type CollectionRule struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	Database   string `json:"database"`
	Table      string `json:"table"`
	Field      string `json:"field"`
	Condition  string `json:"condition"`
	Threshold  string `json:"threshold,omitempty"`
	TimeWindow string `json:"timeWindow"`
	LastRun    string `json:"lastRun"`
	Status     string `json:"status"`
}

type OverviewMetric struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Trend string `json:"trend"`
	Unit  string `json:"unit,omitempty"`
}

type AlertItem struct {
	Title     string `json:"title"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type InspectionTask struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Owner       string    `json:"owner"`
	Status      string    `json:"status"`
	Progress    int       `json:"progress"`
	LastUpdated time.Time `json:"lastUpdated"`
}

type SystemConfig struct {
	PlatformName     string   `json:"platformName"`
	Environment      string   `json:"environment"`
	Responsible      string   `json:"responsible"`
	NotificationMail string   `json:"notificationMail"`
	SMSReceiver      string   `json:"smsReceiver,omitempty"`
	SMTPHost         string   `json:"smtpHost,omitempty"`
	SMTPPort         string   `json:"smtpPort,omitempty"`
	OpenTelemetry    string   `json:"openTelemetry,omitempty"`
	AlertWebhook     string   `json:"alertWebhook,omitempty"`
	RequiredFields   []string `json:"requiredFields,omitempty"`
	OptionalFields   []string `json:"optionalFields,omitempty"`
}
