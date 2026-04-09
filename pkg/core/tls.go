package core

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sort"
	"time"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── Test-replaceable vars ───────────────────────────────────────────────────

var nowFunc = time.Now
var createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
	return cloudflare.NewOriginCA(apiKey).CreateCert(ctx, domains)
}
var findOriginCertFunc = func(ctx context.Context, apiKey, zoneID string, hostnames []string) (*cloudflare.OriginCert, error) {
	ca := cloudflare.NewOriginCA(apiKey)
	found, err := ca.FindCertByHostnames(ctx, zoneID, hostnames)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, nil
	}
	return &cloudflare.OriginCert{ID: found.ID, Certificate: found.Certificate}, nil
}
var revokeOriginCertFunc = func(ctx context.Context, apiKey, certID string) error {
	return cloudflare.NewOriginCA(apiKey).RevokeCert(ctx, certID)
}

// ── TLS material resolution ─────────────────────────────────────────────────

// resolveTLSMaterial resolves cert+key+certID for the given TLS mode and domain set.
// Decoupled from request types — called by both IngressSet and legacy IngressApply.
func resolveTLSMaterial(ctx context.Context, dns ProviderRef, edgeProvider string, certPEM, keyPEM string, tlsMode ingressTLSMode, allDomains []string, out Output, ssh utils.SSHClient, ns string) (string, string, string, error) {
	switch tlsMode {
	case tlsACME:
		return "", "", "", nil
	case tlsProvided:
		return certPEM, keyPEM, "", nil
	case tlsEdgeOrigin:
		if edgeProvider != "" && edgeProvider != "cloudflare" {
			return "", "", "", fmt.Errorf("tls mode %q currently requires edge provider cloudflare (current: %s)", tlsEdgeOrigin, edgeProvider)
		}
		if dns.Name != "cloudflare" {
			return "", "", "", fmt.Errorf("tls mode %q currently requires Cloudflare as DNS provider (current: %s)", tlsEdgeOrigin, dns.Name)
		}

		apiKey := dns.Creds["api_key"]
		zoneID := dns.Creds["zone_id"]

		// Try reuse from k8s secret — cert + key are both local.
		if cp, kp, ok := loadReusableOriginCert(ctx, ssh, ns, allDomains); ok {
			certID := kube.GetTLSSecretAnnotation(ctx, ssh, ns, kube.CaddyTLSSecretName, utils.OriginCAAnnotation)
			out.Success("origin cert reused")
			return cp, kp, certID, nil
		}

		// Not reusable — revoke old cert before creating a new one.
		oldCertID := kube.GetTLSSecretAnnotation(ctx, ssh, ns, kube.CaddyTLSSecretName, utils.OriginCAAnnotation)
		if oldCertID != "" {
			out.Progress("revoking old Origin CA certificate")
			if err := revokeOriginCertFunc(ctx, apiKey, oldCertID); err != nil {
				return "", "", "", fmt.Errorf("revoke Origin CA cert %s: %w — old cert preserved, retry after fixing", oldCertID, err)
			}
		} else if zoneID != "" {
			// Legacy fallback: no annotation, try to find and revoke by hostname match.
			if found, err := findOriginCertFunc(ctx, apiKey, zoneID, allDomains); err == nil && found != nil {
				out.Progress("revoking orphaned Origin CA certificate")
				if err := revokeOriginCertFunc(ctx, apiKey, found.ID); err != nil {
					return "", "", "", fmt.Errorf("revoke Origin CA cert %s: %w — old cert preserved, retry after fixing", found.ID, err)
				}
			}
		}

		out.Progress("generating Cloudflare Origin CA certificate")
		originCert, err := createOriginCertFunc(ctx, apiKey, allDomains)
		if err != nil {
			return "", "", "", fmt.Errorf("origin ca cert: %w", err)
		}
		out.Success("origin cert ready")
		return originCert.Certificate, originCert.PrivateKey, originCert.ID, nil
	default:
		return "", "", "", fmt.Errorf("unsupported ingress tls mode: %s", tlsMode)
	}
}

// ── Origin cert helpers ─────────────────────────────────────────────────────

func loadReusableOriginCert(ctx context.Context, ssh utils.SSHClient, ns string, domains []string) (string, string, bool) {
	certPEM, err := kube.GetSecretValue(ctx, ssh, ns, kube.CaddyTLSSecretName, "tls.crt")
	if err != nil {
		return "", "", false
	}
	keyPEM, err := kube.GetSecretValue(ctx, ssh, ns, kube.CaddyTLSSecretName, "tls.key")
	if err != nil {
		return "", "", false
	}
	if !originCertMatchesDomains(certPEM, domains) {
		return "", "", false
	}
	return certPEM, keyPEM, true
}

func originCertMatchesDomains(certPEM string, domains []string) bool {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if !cert.NotAfter.After(nowFunc()) {
		return false
	}

	certDomains := append([]string(nil), cert.DNSNames...)
	if len(certDomains) == 0 && cert.Subject.CommonName != "" {
		certDomains = append(certDomains, cert.Subject.CommonName)
	}
	sort.Strings(certDomains)

	expected := append([]string(nil), domains...)
	sort.Strings(expected)
	if len(certDomains) != len(expected) {
		return false
	}
	for i := range certDomains {
		if certDomains[i] != expected[i] {
			return false
		}
	}
	return true
}

// extractCertDomains returns the DNS names from an X.509 certificate PEM.
func extractCertDomains(certPEM string) []string {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	domains := append([]string(nil), cert.DNSNames...)
	if len(domains) == 0 && cert.Subject.CommonName != "" {
		domains = append(domains, cert.Subject.CommonName)
	}
	return domains
}
