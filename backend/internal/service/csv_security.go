package service

import "strings"

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func safeCSVRecord(values []string) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = safeCSVCell(value)
	}
	return result
}
