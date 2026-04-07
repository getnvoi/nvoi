package cloudflare

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net/http"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// OriginCAClient manages Cloudflare Origin CA certificates.
type OriginCAClient struct {
	api *utils.HTTPClient
}

// NewOriginCA creates a client for the Cloudflare Origin CA API.
func NewOriginCA(apiKey string) *OriginCAClient {
	return &OriginCAClient{
		api: &utils.HTTPClient{
			BaseURL: cfBaseURL,
			SetAuth: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+apiKey)
			},
			Label: "cloudflare origin ca",
		},
	}
}

// OriginCert holds a PEM-encoded certificate and private key.
type OriginCert struct {
	Certificate string // PEM
	PrivateKey  string // PEM
}

// EnsureCert creates an Origin CA certificate for the given hostnames.
// Returns a 15-year ECDSA cert signed by Cloudflare's Origin CA.
func (c *OriginCAClient) EnsureCert(ctx context.Context, hostnames []string) (*OriginCert, error) {
	// Check if a valid cert already exists for these hostnames
	existing, err := c.findExistingCert(ctx, hostnames)
	if err == nil && existing != "" {
		// Can't retrieve the private key for existing certs — need to generate fresh.
		// Cloudflare doesn't store private keys. We always generate locally.
	}

	// Generate ECDSA private key
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	// Generate CSR
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: hostnames[0]},
	}, privKey)
	if err != nil {
		return nil, fmt.Errorf("create CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})

	// Request cert from Cloudflare Origin CA
	var resp struct {
		Result struct {
			Certificate string `json:"certificate"`
		} `json:"result"`
	}
	err = c.api.Do(ctx, "POST", "/certificates", map[string]any{
		"hostnames":          hostnames,
		"requested_validity": 5475, // 15 years in days
		"request_type":       "origin-ecc",
		"csr":                string(csrPEM),
	}, &resp)
	if err != nil {
		return nil, fmt.Errorf("create origin cert: %w", err)
	}

	// Encode private key to PEM
	keyBytes, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	return &OriginCert{
		Certificate: resp.Result.Certificate,
		PrivateKey:  string(keyPEM),
	}, nil
}

// findExistingCert checks if there's already a valid Origin CA cert for these hostnames.
// Returns the cert ID if found, empty string if not.
func (c *OriginCAClient) findExistingCert(ctx context.Context, hostnames []string) (string, error) {
	if len(hostnames) == 0 {
		return "", fmt.Errorf("no hostnames")
	}
	var resp struct {
		Result []struct {
			ID        string   `json:"id"`
			Hostnames []string `json:"hostnames"`
		} `json:"result"`
	}
	err := c.api.Do(ctx, "GET", fmt.Sprintf("/certificates?zone_id="), nil, &resp)
	if err != nil {
		return "", err
	}
	// Check for matching hostnames
	want := map[string]bool{}
	for _, h := range hostnames {
		want[h] = true
	}
	for _, cert := range resp.Result {
		match := true
		for _, h := range hostnames {
			found := false
			for _, ch := range cert.Hostnames {
				if ch == h || ch == "*."+h {
					found = true
					break
				}
			}
			if !found {
				match = false
				break
			}
		}
		if match {
			return cert.ID, nil
		}
	}
	return "", fmt.Errorf("no matching cert")
}
