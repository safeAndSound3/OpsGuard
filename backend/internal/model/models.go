package model

import "time"

type DataSource struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Host        string            `json:"host"`
	Port        string            `json:"port"`
	Username    string            `json:"username,omitempty"`
	Password    string            `json:"password,omitempty"`
	Database    string            `json:"database,omitempty"`
	Description string            `json:"description,omitempty"`
	Remark      string            `json:"remark,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
	Enabled     bool              `json:"enabled"`
	Status      string            `json:"status"`
	LastTest    string            `json:"lastTest"`
	Tags        []string          `json:"tags,omitempty"`
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

type MySQLInstanceStatus struct {
	SourceID          string    `json:"sourceId"`
	SourceName        string    `json:"sourceName"`
	Host              string    `json:"host"`
	Port              string    `json:"port"`
	Status            string    `json:"status"`
	Version           string    `json:"version,omitempty"`
	UptimeSeconds     int64     `json:"uptimeSeconds"`
	ThreadsConnected  int64     `json:"threadsConnected"`
	MaxConnections    int64     `json:"maxConnections"`
	SlowQueries       int64     `json:"slowQueries"`
	Questions         int64     `json:"questions"`
	DatabaseSizeBytes int64     `json:"databaseSizeBytes"`
	ReplicaStatus     string    `json:"replicaStatus,omitempty"`
	LastError         string    `json:"lastError,omitempty"`
	LastCollectedAt   time.Time `json:"lastCollectedAt"`
}

type MySQLMetricSnapshot struct {
	ID          int64             `json:"id"`
	SourceID    string            `json:"sourceId"`
	CollectedAt time.Time         `json:"collectedAt"`
	Metrics     map[string]string `json:"metrics"`
}

type MySQLSlowQuerySample struct {
	ID               int64     `json:"id"`
	SourceID         string    `json:"sourceId"`
	SchemaName       string    `json:"schemaName,omitempty"`
	Digest           string    `json:"digest,omitempty"`
	QueryText        string    `json:"queryText"`
	Count            int64     `json:"count"`
	TotalLatencyMs   float64   `json:"totalLatencyMs"`
	AverageLatencyMs float64   `json:"averageLatencyMs"`
	MaxLatencyMs     float64   `json:"maxLatencyMs"`
	RowsExamined     int64     `json:"rowsExamined"`
	RowsSent         int64     `json:"rowsSent"`
	FirstSeen        time.Time `json:"firstSeen,omitempty"`
	LastSeen         time.Time `json:"lastSeen,omitempty"`
	CollectedAt      time.Time `json:"collectedAt"`
}
