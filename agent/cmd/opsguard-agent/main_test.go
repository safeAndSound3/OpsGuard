package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthAcceptsAnyConfiguredToken(t *testing.T) {
	request := func(token string) *http.Request {
		req, err := http.NewRequest(http.MethodGet, "http://agent/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return req
	}
	tokens := []string{"token-one", "token-two"}
	for _, valid := range tokens {
		if !auth(request(valid), tokens) {
			t.Fatalf("configured token %q should authenticate", valid)
		}
	}
	if auth(request(""), tokens) {
		t.Fatal("missing token should be rejected")
	}
	if auth(request("token-one "), tokens) {
		t.Fatal("whitespace-suffixed token should be rejected")
	}
	if auth(request("token-three"), tokens) {
		t.Fatal("unknown token should be rejected")
	}
	if auth(request("Token-One"), tokens) {
		t.Fatal("case mismatch should be rejected")
	}
}

func TestAgentCertificateFingerprintGeneratesAndIsStable(t *testing.T) {
	dir := t.TempDir()
	cfg := config{
		tlsCertFile: filepath.Join(dir, "agent.crt"),
		tlsKeyFile:  filepath.Join(dir, "agent.key"),
	}
	first, err := agentCertificateFingerprint(cfg)
	if err != nil {
		t.Fatalf("generate fingerprint: %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("expected 64-hex-char fingerprint, got %q", first)
	}
	for _, path := range []string{cfg.tlsCertFile, cfg.tlsKeyFile} {
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("expected certificate file %s to exist, err=%v", path, err)
		}
	}
	second, err := agentCertificateFingerprint(cfg)
	if err != nil {
		t.Fatalf("recompute fingerprint: %v", err)
	}
	if first != second {
		t.Fatalf("fingerprint must be stable across restarts: %s != %s", first, second)
	}
}

func TestAgentCertificateFingerprintRejectsCorruptedKey(t *testing.T) {
	dir := t.TempDir()
	cfg := config{
		tlsCertFile: filepath.Join(dir, "agent.crt"),
		tlsKeyFile:  filepath.Join(dir, "agent.key"),
	}
	if _, err := agentCertificateFingerprint(cfg); err != nil {
		t.Fatalf("fresh generation should succeed: %v", err)
	}
	if err := os.WriteFile(cfg.tlsCertFile, []byte("not a pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := agentCertificateFingerprint(cfg); err == nil || !strings.Contains(err.Error(), "PEM") {
		t.Fatalf("corrupted certificate should fail with a PEM error, got %v", err)
	}
}

func TestAgentCertificateRejectsMissingKeyWithoutChangingFingerprint(t *testing.T) {
	dir := t.TempDir()
	cfg := config{tlsCertFile: filepath.Join(dir, "agent.crt"), tlsKeyFile: filepath.Join(dir, "agent.key")}
	before, err := agentCertificateFingerprint(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cfg.tlsKeyFile); err != nil {
		t.Fatal(err)
	}
	if _, err := agentCertificateFingerprint(cfg); err == nil {
		t.Fatal("missing key must not silently replace the certificate")
	}
	data, err := os.ReadFile(cfg.tlsCertFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || before == "" {
		t.Fatal("existing certificate must remain available for recovery")
	}
}

func TestLimitedCommandOutput(t *testing.T) {
	out := &limitedCommandOutput{limit: 5}
	for _, part := range []string{"abc", "defgh"} {
		if n, err := out.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("Write returned %d, %v", n, err)
		}
	}
	if got := out.String(); got != "abcde\n... truncated" {
		t.Fatalf("unexpected bounded output: %q", got)
	}
	if len(out.data) != 5 {
		t.Fatalf("retained %d bytes, want 5", len(out.data))
	}
}
