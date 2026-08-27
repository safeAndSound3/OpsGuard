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

type AlertNotification struct {
	ID          string     `json:"id"`
	RuleID      string     `json:"ruleId"`
	RuleName    string     `json:"ruleName"`
	Source      string     `json:"source"`
	Database    string     `json:"database"`
	Table       string     `json:"table"`
	Field       string     `json:"field"`
	Severity    string     `json:"severity"`
	Status      string     `json:"status"`
	Message     string     `json:"message"`
	Unread      bool       `json:"unread"`
	FirstSeenAt time.Time  `json:"firstSeenAt"`
	LastSeenAt  time.Time  `json:"lastSeenAt"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
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

type RedisInstanceStatus struct {
	SourceID            string    `json:"sourceId"`
	SourceName          string    `json:"sourceName"`
	Host                string    `json:"host"`
	Port                string    `json:"port"`
	Status              string    `json:"status"`
	Version             string    `json:"version,omitempty"`
	UptimeSeconds       int64     `json:"uptimeSeconds"`
	ConnectedClients    int64     `json:"connectedClients"`
	BlockedClients      int64     `json:"blockedClients"`
	UsedMemory          int64     `json:"usedMemory"`
	MaxMemory           int64     `json:"maxMemory"`
	MemoryFragmentation float64   `json:"memoryFragmentation"`
	OpsPerSecond        int64     `json:"opsPerSecond"`
	TotalCommands       int64     `json:"totalCommands"`
	HitRate             float64   `json:"hitRate"`
	EvictedKeys         int64     `json:"evictedKeys"`
	ExpiredKeys         int64     `json:"expiredKeys"`
	RejectedConnections int64     `json:"rejectedConnections"`
	SlowlogLength       int64     `json:"slowlogLength"`
	KeyCount            int64     `json:"keyCount"`
	Role                string    `json:"role,omitempty"`
	LastError           string    `json:"lastError,omitempty"`
	LastCollectedAt     time.Time `json:"lastCollectedAt"`
}

type RedisMetricSnapshot struct {
	ID          int64             `json:"id"`
	SourceID    string            `json:"sourceId"`
	CollectedAt time.Time         `json:"collectedAt"`
	Metrics     map[string]string `json:"metrics"`
}

type SSHInstanceStatus struct {
	SourceID        string    `json:"sourceId"`
	SourceName      string    `json:"sourceName"`
	Host            string    `json:"host"`
	Port            string    `json:"port"`
	Status          string    `json:"status"`
	Hostname        string    `json:"hostname,omitempty"`
	Kernel          string    `json:"kernel,omitempty"`
	UptimeSeconds   int64     `json:"uptimeSeconds"`
	CPUUsagePercent float64   `json:"cpuUsagePercent"`
	Load1           float64   `json:"load1"`
	Load5           float64   `json:"load5"`
	Load15          float64   `json:"load15"`
	MemoryUsed      int64     `json:"memoryUsed"`
	MemoryTotal     int64     `json:"memoryTotal"`
	MemoryPercent   float64   `json:"memoryPercent"`
	DiskUsed        int64     `json:"diskUsed"`
	DiskTotal       int64     `json:"diskTotal"`
	DiskPercent     float64   `json:"diskPercent"`
	ProcessCount    int64     `json:"processCount"`
	TCPConnections  int64     `json:"tcpConnections"`
	LastError       string    `json:"lastError,omitempty"`
	LastCollectedAt time.Time `json:"lastCollectedAt"`
}

type SSHMetricSnapshot struct {
	ID          int64             `json:"id"`
	SourceID    string            `json:"sourceId"`
	CollectedAt time.Time         `json:"collectedAt"`
	Metrics     map[string]string `json:"metrics"`
}

type ExternalMonitorConfig struct {
	PrometheusURL             string `json:"prometheusUrl,omitempty"`
	PrometheusConfigured      bool   `json:"prometheusConfigured"`
	PrometheusTokenConfigured bool   `json:"prometheusTokenConfigured"`
	GrafanaURL                string `json:"grafanaUrl,omitempty"`
	GrafanaConfigured         bool   `json:"grafanaConfigured"`
	GrafanaTokenConfigured    bool   `json:"grafanaTokenConfigured"`
}

type PrometheusAlert struct {
	Name        string            `json:"name"`
	State       string            `json:"state"`
	Severity    string            `json:"severity,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Description string            `json:"description,omitempty"`
	ActiveAt    string            `json:"activeAt,omitempty"`
	Value       string            `json:"value,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type PrometheusMetric struct {
	Name string `json:"name"`
}

type GrafanaDashboard struct {
	UID         string   `json:"uid"`
	Title       string   `json:"title"`
	URI         string   `json:"uri,omitempty"`
	URL         string   `json:"url,omitempty"`
	FolderTitle string   `json:"folderTitle,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}
