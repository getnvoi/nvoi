package github

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/nacl/box"
)

// sealSecret encrypts `plaintext` with the GitHub Actions "encrypted
// value" format: libsodium sealed-box against the repo's x25519 public
// key, base64-encoded. This is the required wire format for
// PUT /repos/{owner}/{repo}/actions/secrets/{name}.
//
// Sealed-box = ephemeral keypair + NaCl box anonymous encryption. The
// sender public key is prepended to the ciphertext so the recipient
// (GitHub's runner, which holds the matching private key) can decrypt
// without any prior session. We never see the private key.
//
// pubKeyB64 is the `key` field returned by
// GET /repos/{owner}/{repo}/actions/secrets/public-key — 32 raw bytes
// after base64 decoding. Curve25519 requires exactly 32 bytes; a
// different length is a server bug or a MITM'd response, so we error
// rather than silently pad.
func sealSecret(pubKeyB64, plaintext string) (string, error) {
	pk, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		return "", fmt.Errorf("decode public key: %w", err)
	}
	if len(pk) != 32 {
		return "", fmt.Errorf("invalid public key length: got %d, want 32", len(pk))
	}
	var recipient [32]byte
	copy(recipient[:], pk)

	sealed, err := box.SealAnonymous(nil, []byte(plaintext), &recipient, rand.Reader)
	if err != nil {
		return "", fmt.Errorf("seal: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}
