package service

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"monitor-platform/internal/model"
)

// SSHHostKeyPolicy controls how the platform verifies SSH server host keys.
//   - tofu (default): the first connection records the host key fingerprint,
//     later connections must match it; mismatches are rejected as a possible
//     man-in-the-middle attack.
//   - strict: every connection is verified against a known_hosts file; hosts
//     that are missing from the file are rejected.
//   - off: legacy behaviour with no host key verification (logs a warning).
type SSHHostKeyPolicy struct {
	Mode           string
	KnownHostsFile string
}

var sshHostKeyState = struct {
	sync.RWMutex
	mode           string
	knownHostsFile string
}{mode: "tofu"}

// ConfigureSSHHostKeyPolicy validates and applies the runtime policy.
func ConfigureSSHHostKeyPolicy(policy SSHHostKeyPolicy) error {
	mode := strings.ToLower(strings.TrimSpace(policy.Mode))
	switch mode {
	case "":
		mode = "tofu"
	case "tofu", "strict", "off":
	default:
		return fmt.Errorf("invalid SSH_HOST_KEY_MODE %q (want tofu, strict or off)", policy.Mode)
	}
	if mode == "strict" && strings.TrimSpace(policy.KnownHostsFile) == "" {
		return errors.New("SSH_HOST_KEY_MODE=strict requires SSH_KNOWN_HOSTS to be set")
	}
	sshHostKeyState.Lock()
	sshHostKeyState.mode = mode
	sshHostKeyState.knownHostsFile = strings.TrimSpace(policy.KnownHostsFile)
	sshHostKeyState.Unlock()
	return nil
}

func currentSSHHostKeyMode() string {
	sshHostKeyState.RLock()
	defer sshHostKeyState.RUnlock()
	return sshHostKeyState.mode
}

// hostKeyCallback builds the HostKeyCallback used for a traditional SSH data
// source based on the configured policy. Agent data sources are unaffected.
func hostKeyCallback(ds model.DataSource) (ssh.HostKeyCallback, error) {
	switch currentSSHHostKeyMode() {
	case "strict":
		sshHostKeyState.RLock()
		file := sshHostKeyState.knownHostsFile
		sshHostKeyState.RUnlock()
		callback, err := knownhosts.New(file)
		if err != nil {
			return nil, fmt.Errorf("load SSH_KNOWN_HOSTS %q: %w", file, err)
		}
		return callback, nil
	case "off":
		return ssh.InsecureIgnoreHostKey(), nil
	default:
		return tofuHostKeyCallback(ds), nil
	}
}

// tofuHostKeyCallback implements trust-on-first-use host key verification.
// A stored empty fingerprint records the current key on first contact; any
// later mismatch is rejected as a potential man-in-the-middle attack.
func tofuHostKeyCallback(ds model.DataSource) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		remoteFP := ssh.FingerprintSHA256(key)
		storedFP := strings.TrimSpace(ds.Options["sshHostKeyFp"])
		if storedFP == "" {
			// 未保存的数据源（测试连接阶段）没有 ID，无法持久化指纹：
			// 直接放行，保存后的首次真实连接再记录。
			if ds.ID == "" {
				return nil
			}
			if err := storeSSHHostKeyFingerprint(ds.ID, remoteFP); err != nil {
				return fmt.Errorf("记录 SSH 主机指纹失败: %w", err)
			}
			return nil
		}
		if subtle.ConstantTimeCompare([]byte(storedFP), []byte(remoteFP)) != 1 {
			return fmt.Errorf("SSH 主机密钥指纹不匹配（已记录 %s，实际 %s），可能遭受中间人攻击；确认主机密钥确实更换后，请在数据源执行“重置主机密钥指纹”", storedFP, remoteFP)
		}
		return nil
	}
}

var sshHostKeyFingerprintMu sync.Mutex

// storeSSHHostKeyFingerprint persists the observed fingerprint, skipping the
// write when another caller has already recorded it (first-connect races
// between the metric collector and connection tests).
func storeSSHHostKeyFingerprint(id, fingerprint string) error {
	sshHostKeyFingerprintMu.Lock()
	defer sshHostKeyFingerprintMu.Unlock()
	existing, err := GetDataSourceByID(id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(existing.Options["sshHostKeyFp"]) != "" {
		return nil
	}
	return setDataSourceOption(id, "sshHostKeyFp", fingerprint)
}

// ResetSSHHostKeyFingerprint clears the stored host key fingerprint so the
// next connection re-trusts the key (used when a host key legitimately changed).
func ResetSSHHostKeyFingerprint(id string) error {
	existing, err := GetDataSourceByID(id)
	if err != nil {
		return errors.New("data source not found")
	}
	if !strings.EqualFold(existing.Type, "ssh") || isAgentSSH(existing) || isLocalSSH(existing) {
		return errors.New("仅传统 SSH 数据源支持重置主机密钥指纹")
	}
	return setDataSourceOption(id, "sshHostKeyFp", "")
}
