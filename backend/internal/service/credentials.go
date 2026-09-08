package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const encryptedCredentialPrefix = "enc:v1:"

var credentialCipher cipher.AEAD

func initializeCredentialEncryption() error {
	raw := strings.TrimSpace(os.Getenv("OPSGUARD_ENCRYPTION_KEY"))
	if raw == "" {
		return errors.New("OPSGUARD_ENCRYPTION_KEY is required to protect stored credentials")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return errors.New("OPSGUARD_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	credentialCipher, err = cipher.NewGCM(block)
	return err
}

func encryptCredential(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if strings.HasPrefix(plain, encryptedCredentialPrefix) {
		return plain, nil
	}
	if credentialCipher == nil {
		return "", errors.New("credential encryption is not initialized")
	}
	nonce := make([]byte, credentialCipher.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := credentialCipher.Seal(nil, nonce, []byte(plain), nil)
	return encryptedCredentialPrefix + base64.StdEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

func decryptCredential(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, encryptedCredentialPrefix) {
		return value, nil
	}
	if credentialCipher == nil {
		return "", errors.New("credential encryption is not initialized")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encryptedCredentialPrefix))
	if err != nil {
		return "", fmt.Errorf("invalid encrypted credential: %w", err)
	}
	nonceSize := credentialCipher.NonceSize()
	if len(payload) < nonceSize {
		return "", errors.New("invalid encrypted credential payload")
	}
	plain, err := credentialCipher.Open(nil, payload[:nonceSize], payload[nonceSize:], nil)
	if err != nil {
		return "", errors.New("unable to decrypt stored credential")
	}
	return string(plain), nil
}

func hashUserPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func verifyUserPassword(stored string, password string) bool {
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil
	}
	return stored == password
}
