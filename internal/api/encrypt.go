package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

var encKey []byte

// InitEncryption loads the encryption key from ENCRYPTION_KEY env var.
// Must be called before any encrypt/decrypt operations.
func InitEncryption() error {
	keyHex := os.Getenv("ENCRYPTION_KEY")
	if keyHex == "" {
		return fmt.Errorf("ENCRYPTION_KEY is required")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return fmt.Errorf("ENCRYPTION_KEY must be hex-encoded: %w", err)
	}
	if len(key) != 32 {
		return fmt.Errorf("ENCRYPTION_KEY must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}
	encKey = key
	return nil
}

// Encrypt encrypts plaintext using AES-256-GCM. Returns hex-encoded ciphertext.
func Encrypt(plaintext string) (string, error) {
	if encKey == nil {
		return "", fmt.Errorf("encryption not initialized")
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts hex-encoded AES-256-GCM ciphertext.
func Decrypt(ciphertextHex string) (string, error) {
	if encKey == nil {
		return "", fmt.Errorf("encryption not initialized")
	}

	ciphertext, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext: %w", err)
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt failed: %w", err)
	}

	return string(plaintext), nil
}
