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
	CORSAllowedOrigins   []string
	SessionCookieSecure  bool
	SessionTTL           string
	OutboundAllowedHosts []string
	OutboundAllowedCIDRs []string
	AllowLoopbackTargets bool
}

func Load() AppConfig {
	// A deployment can point to an explicit file, while the default location
	// keeps configuration next to the backend instead of in source code.
	if path := strings.TrimSpace(os.Getenv("OPSGUARD_CONFIG")); path != "" {
		loadEnvFile(path)
	}
	loadEnvFile("config/opsguard.conf")
	// Keep existing local installations working during the config-file migration.
	loadEnvFile(".env")
	return AppConfig{
		Host:                 getEnv("HOST", "0.0.0.0"),
		Port:                 getEnv("PORT", "8030"),
		Env:                  getEnv("ENV", "development"),
		CORSAllowedOrigins:   splitCSV(os.Getenv("CORS_ALLOWED_ORIGINS")),
		SessionCookieSecure:  strings.EqualFold(getEnv("SESSION_COOKIE_SECURE", "false"), "true"),
		SessionTTL:           getEnv("SESSION_TTL", "12h"),
		OutboundAllowedHosts: splitCSV(os.Getenv("OUTBOUND_ALLOWED_HOSTS")),
		OutboundAllowedCIDRs: splitCSV(os.Getenv("OUTBOUND_ALLOWED_CIDRS")),
		AllowLoopbackTargets: strings.EqualFold(getEnv("OUTBOUND_ALLOW_LOOPBACK", "false"), "true"),
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
