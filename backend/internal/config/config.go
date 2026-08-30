package config

import (
	"bufio"
	"os"
	"strings"
)

type AppConfig struct {
	Host string
	Port string
	Env  string
}

func Load() AppConfig {
	loadEnvFile(".env")
	return AppConfig{
		Host: getEnv("HOST", "0.0.0.0"),
		Port: getEnv("PORT", "8030"),
		Env:  getEnv("ENV", "development"),
	}
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
