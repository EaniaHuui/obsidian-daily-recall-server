package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

type SecretBox struct {
	masterKey []byte
}

func NewSecretBox(masterKeyHex string) (SecretBox, error) {
	if masterKeyHex == "" {
		return SecretBox{}, nil
	}

	key, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return SecretBox{}, fmt.Errorf("decode master key: %w", err)
	}
	if len(key) != 32 {
		return SecretBox{}, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}

	return SecretBox{masterKey: key}, nil
}

func (s SecretBox) EncryptString(value string) (string, error) {
	if value == "" || len(s.masterKey) == 0 {
		return value, nil
	}

	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return "", fmt.Errorf("new aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(value), nil)
	payload := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

func (s SecretBox) DecryptString(value string) (string, error) {
	if value == "" || len(s.masterKey) == 0 {
		return value, nil
	}

	payload, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return "", fmt.Errorf("new aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	if len(payload) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt value: %w", err)
	}

	return string(plaintext), nil
}
