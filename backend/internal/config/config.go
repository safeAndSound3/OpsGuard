package config

import (
	"bufio"
	"os"
	"strings"
)

type AppConfig struct {
	Host                 string
	Port                 string
	Env                  string
	TLSEnabled           bool
	TLSCertFile          string
	TLSKeyFile           string
	TLSServerNames       []string
	CORSAllowedOrigins   []string
	SessionTTL           string
	OutboundAllowedHosts []string
	OutboundAllowedCIDRs []string
	AllowLoopbackTargets bool
	SSHHostKeyMode       string
	SSHKnownHosts        string
	LogFile              string
}

func Load() AppConfig {
	// A deployment can point to an explicit file, while the default location
	// keeps configuration next to the backend instead of in source code.
	if path := strings.TrimSpace(os.Getenv("OPSGUARD_CONFIG")); path != "" {
		loadEnvFile(path)
	}
	// Resolve both the backend working directory and the repository root. This
	// keeps `go run ./cmd/server` and `go run ./backend/cmd/server` equivalent.
	for _, path := range []string{"config/opsguard.conf", "backend/config/opsguard.conf"} {
		loadEnvFile(path)
	}
	// Keep existing local installations working during the config-file migration.
	for _, path := range []string{".env", "backend/.env"} {
		loadEnvFile(path)
	}
	return AppConfig{
		Host:                 getEnv("HOST", "0.0.0.0"),
		Port:                 getEnv("PORT", "8030"),
		Env:                  getEnv("ENV", "development"),
		TLSEnabled:           strings.EqualFold(getEnv("TLS_ENABLED", "true"), "true"),
		TLSCertFile:          getEnv("TLS_CERT_FILE", "certs/opsguard.crt"),
		TLSKeyFile:           getEnv("TLS_KEY_FILE", "certs/opsguard.key"),
		TLSServerNames:       splitCSV(os.Getenv("TLS_SERVER_NAMES")),
		CORSAllowedOrigins:   splitCSV(os.Getenv("CORS_ALLOWED_ORIGINS")),
		SessionTTL:           getEnv("SESSION_TTL", "12h"),
		OutboundAllowedHosts: splitCSV(os.Getenv("OUTBOUND_ALLOWED_HOSTS")),
		OutboundAllowedCIDRs: splitCSV(os.Getenv("OUTBOUND_ALLOWED_CIDRS")),
		AllowLoopbackTargets: strings.EqualFold(getEnv("OUTBOUND_ALLOW_LOOPBACK", "false"), "true"),
		SSHHostKeyMode:       getEnv("SSH_HOST_KEY_MODE", "tofu"),
		SSHKnownHosts:        strings.TrimSpace(os.Getenv("SSH_KNOWN_HOSTS")),
		LogFile:              getEnv("LOG_FILE", "logs/opsguard-server.log"),
	}
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func loadEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
