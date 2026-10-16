package service

import "testing"

func TestSafeCSVCell(t *testing.T) {
	for input, want := range map[string]string{
		"=SUM(1,2)":  "'=SUM(1,2)",
		" +cmd":      "' +cmd",
		"\t@formula": "'\t@formula",
		"normal":     "normal",
		"":           "",
	} {
		if got := safeCSVCell(input); got != want {
			t.Errorf("safeCSVCell(%q) = %q, want %q", input, got, want)
		}
	}
}
