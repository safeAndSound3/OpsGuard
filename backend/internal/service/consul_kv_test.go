package service

import "testing"

func TestValidateConsulKVFormat(t *testing.T) {
	if err := validateConsulKVFormat(`{"name":"ok"}`, "json"); err != nil {
		t.Fatalf("valid JSON rejected: %v", err)
	}
	if err := validateConsulKVFormat(`{"name":`, "json"); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if err := validateConsulKVFormat("service = {\n name = \"api\"\n}", "hcl"); err != nil {
		t.Fatalf("valid HCL structure rejected: %v", err)
	}
	if err := validateConsulKVFormat("key: [", "yaml"); err == nil {
		t.Fatal("invalid YAML structure accepted")
	}
}
