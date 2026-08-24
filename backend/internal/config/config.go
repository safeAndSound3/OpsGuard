package config

import "os"

type AppConfig struct {
	Host string
	Port string
	Env  string
}

func Load() AppConfig {
	return AppConfig{
		Host: getEnv("HOST", "0.0.0.0"),
		Port: getEnv("PORT", "8030"),
		Env:  getEnv("ENV", "development"),
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
