package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"monitor-platform/internal/model"
)

func TestConfigureSSHHostKeyPolicyRejectsInvalidMode(t *testing.T) {
	err := ConfigureSSHHostKeyPolicy(SSHHostKeyPolicy{Mode: "sloppy"})
	if err == nil || !strings.Contains(err.Error(), "SSH_HOST_KEY_MODE") {
		t.Fatalf("expected invalid mode error, got %v", err)
	}
}

func TestConfigureSSHHostKeyPolicyStrictRequiresKnownHosts(t *testing.T) {
	if err := ConfigureSSHHostKeyPolicy(SSHHostKeyPolicy{Mode: "strict"}); err == nil {
		t.Fatal("expected strict mode without SSH_KNOWN_HOSTS to fail")
	}
	if err := ConfigureSSHHostKeyPolicy(SSHHostKeyPolicy{Mode: "strict", KnownHostsFile: "testdata/known_hosts"}); err != nil {
		t.Fatalf("strict with known hosts file should be accepted: %v", err)
	}
}

func TestConfigureSSHHostKeyPolicyDefaultsToTofu(t *testing.T) {
	if err := ConfigureSSHHostKeyPolicy(SSHHostKeyPolicy{Mode: ""}); err != nil {
		t.Fatalf("empty mode should default to tofu: %v", err)
	}
	if mode := currentSSHHostKeyMode(); mode != "tofu" {
		t.Fatalf("expected tofu default, got %q", mode)
	}
	// Restore a deterministic state for other tests.
	if err := ConfigureSSHHostKeyPolicy(SSHHostKeyPolicy{Mode: "tofu"}); err != nil {
		t.Fatal(err)
	}
}

func testSSHPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey
}

func TestTofuHostKeyCallbackAcceptsUnsavedSourceWithoutStoring(t *testing.T) {
	if err := ConfigureSSHHostKeyPolicy(SSHHostKeyPolicy{Mode: "tofu"}); err != nil {
		t.Fatal(err)
	}
	callback := tofuHostKeyCallback(model.DataSource{ID: ""})
	if err := callback("host", nil, testSSHPublicKey(t)); err != nil {
		t.Fatalf("unsaved source (no ID) should be accepted without persisting: %v", err)
	}
}

func TestTofuHostKeyCallbackAcceptsStoredMatchAndRejectsMismatch(t *testing.T) {
	if err := ConfigureSSHHostKeyPolicy(SSHHostKeyPolicy{Mode: "tofu"}); err != nil {
		t.Fatal(err)
	}
	first := testSSHPublicKey(t)
	second := testSSHPublicKey(t)
	// First key matches the stored fingerprint -> accepted.
	ds := model.DataSource{ID: "ds-1", Options: map[string]string{"sshHostKeyFp": ssh.FingerprintSHA256(first)}}
	callback := tofuHostKeyCallback(ds)
	if err := callback("host", nil, first); err != nil {
		t.Fatalf("matching fingerprint should be accepted: %v", err)
	}
	// Second key differs -> rejected as a possible MITM attack.
	err := callback("host", nil, second)
	if err == nil || !strings.Contains(err.Error(), "中间人攻击") {
		t.Fatalf("mismatching fingerprint should be rejected with MITM warning, got %v", err)
	}
}

func TestVerifyAgentCertificate(t *testing.T) {
	cert := testCertificate(t)
	fingerprint := agentCertFingerprint(cert)
	if len(fingerprint) != 64 {
		t.Fatalf("expected 64-hex-char fingerprint, got %q", fingerprint)
	}
	if err := verifyAgentCertificate("", cert); err != nil {
		t.Fatalf("empty expected fingerprint (TOFU) should pass: %v", err)
	}
	if err := verifyAgentCertificate(fingerprint, cert); err != nil {
		t.Fatalf("matching fingerprint should pass: %v", err)
	}
	err := verifyAgentCertificate(strings.Repeat("0", 64), cert)
	if err == nil || !strings.Contains(err.Error(), "指纹不匹配") {
		t.Fatalf("mismatching fingerprint should fail, got %v", err)
	}
	if err := verifyAgentCertificate(fingerprint, nil); err == nil {
		t.Fatal("nil peer certificate should fail")
	}
}

func TestAgentCertFingerprintMatchesSHA256OfDER(t *testing.T) {
	cert := testCertificate(t)
	sum := sha256.Sum256(cert.Raw)
	if agentCertFingerprint(cert) != hex.EncodeToString(sum[:]) {
		t.Fatal("fingerprint must be lowercase hex of the certificate DER SHA-256")
	}
}

func testCertificate(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "opsguard-agent"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
