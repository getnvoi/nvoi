package utils

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// GenerateEd25519Key generates an Ed25519 SSH keypair.
// Returns (PEM-encoded private key, OpenSSH public key, error).
func GenerateEd25519Key() ([]byte, string, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, "", fmt.Errorf("generate ed25519 key: %w", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, "", fmt.Errorf("marshal private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", fmt.Errorf("convert public key: %w", err)
	}
	return privPEM, string(ssh.MarshalAuthorizedKey(sshPub)), nil
}

// DerivePublicKey derives the OpenSSH public key from a PEM-encoded private key.
func DerivePublicKey(pemData []byte) (string, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block")
	}
	if signer, err := ssh.ParsePrivateKey(pemData); err == nil {
		return string(ssh.MarshalAuthorizedKey(signer.PublicKey())), nil
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if edKey, ok := key.(ed25519.PrivateKey); ok {
			if pub, err := ssh.NewPublicKey(edKey.Public()); err == nil {
				return string(ssh.MarshalAuthorizedKey(pub)), nil
			}
		}
	}
	return "", fmt.Errorf("unsupported key format")
}
