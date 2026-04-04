package utils

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

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
