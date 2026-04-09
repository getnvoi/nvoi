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

// ── TLS material resolution ─────────────────────────────────────────────────

// resolveTLSMaterial resolves cert+key+certID. Three branches:
//   - cloudflareManaged → Origin CA cert via Cloudflare
//   - certPEM+keyPEM provided → use as-is
//   - neither → ACME (return empty, Caddy handles it)
func resolveTLSMaterial(ctx context.Context, dns ProviderRef, cloudflareManaged bool, certPEM, keyPEM string, allDomains []string, out Output, ssh utils.SSHClient, ns string, hooks *IngressHooks) (string, string, string, error) {
	if certPEM != "" && keyPEM != "" {
		return certPEM, keyPEM, "", nil
	}
	if !cloudflareManaged {
		return "", "", "", nil // ACME
	}
	if dns.Name != "cloudflare" {
		return "", "", "", fmt.Errorf("cloudflare-managed requires Cloudflare as DNS provider (current: %s)", dns.Name)
	}
	return resolveOriginCACert(ctx, dns.Creds, allDomains, out, ssh, ns, hooks)
}

// resolveOriginCACert handles the Cloudflare Origin CA lifecycle:
// reuse existing cert if domains match, revoke old if not, create new.
func resolveOriginCACert(ctx context.Context, creds map[string]string, domains []string, out Output, ssh utils.SSHClient, ns string, hooks *IngressHooks) (string, string, string, error) {
	apiKey := creds["api_key"]
	zoneID := creds["zone_id"]
	now := hooks.now()
	createCert := hooks.createOriginCert()
	revokeCert := hooks.revokeOriginCert()
	findCert := hooks.findOriginCert()

	// Reuse if cert exists and domains match.
	if cp, kp, ok := loadReusableOriginCert(ctx, ssh, ns, domains, now); ok {
		certID := kube.GetTLSSecretAnnotation(ctx, ssh, ns, kube.CaddyTLSSecretName, utils.OriginCAAnnotation)
		out.Success("origin cert reused")
		return cp, kp, certID, nil
	}

	// Revoke old before creating new.
	if err := revokeOldOriginCert(ctx, apiKey, zoneID, domains, out, ssh, ns, revokeCert, findCert); err != nil {
		return "", "", "", err
	}

	// Create new.
	out.Progress("generating Cloudflare Origin CA certificate")
	cert, err := createCert(ctx, apiKey, domains)
	if err != nil {
		return "", "", "", fmt.Errorf("origin ca cert: %w", err)
	}
	out.Success("origin cert ready")
	return cert.Certificate, cert.PrivateKey, cert.ID, nil
}

// revokeOldOriginCert revokes the previous Origin CA cert if one exists.
// Two lookup paths: annotation (fast), hostname match at Cloudflare (fallback).
func revokeOldOriginCert(
	ctx context.Context, apiKey, zoneID string, domains []string,
	out Output, ssh utils.SSHClient, ns string,
	revokeCert func(context.Context, string, string) error,
	findCert func(context.Context, string, string, []string) (*cloudflare.OriginCert, error),
) error {
	// Fast path: cert ID from annotation.
	oldCertID := kube.GetTLSSecretAnnotation(ctx, ssh, ns, kube.CaddyTLSSecretName, utils.OriginCAAnnotation)
	if oldCertID != "" {
		out.Progress("revoking old Origin CA certificate")
		if err := revokeCert(ctx, apiKey, oldCertID); err != nil {
			return fmt.Errorf("revoke Origin CA cert %s: %w — old cert preserved, retry after fixing", oldCertID, err)
		}
		return nil
	}

	// Fallback: find by hostname match.
	if zoneID != "" {
		if found, err := findCert(ctx, apiKey, zoneID, domains); err == nil && found != nil {
			out.Progress("revoking orphaned Origin CA certificate")
			if err := revokeCert(ctx, apiKey, found.ID); err != nil {
				return fmt.Errorf("revoke Origin CA cert %s: %w — old cert preserved, retry after fixing", found.ID, err)
			}
		}
	}
	return nil
}

// ── Origin cert helpers ─────────────────────────────────────────────────────

func loadReusableOriginCert(ctx context.Context, ssh utils.SSHClient, ns string, domains []string, now func() time.Time) (string, string, bool) {
	certPEM, err := kube.GetSecretValue(ctx, ssh, ns, kube.CaddyTLSSecretName, "tls.crt")
	if err != nil {
		return "", "", false
	}
	keyPEM, err := kube.GetSecretValue(ctx, ssh, ns, kube.CaddyTLSSecretName, "tls.key")
	if err != nil {
		return "", "", false
	}
	if !originCertMatchesDomains(certPEM, domains, now) {
		return "", "", false
	}
	return certPEM, keyPEM, true
}

func originCertMatchesDomains(certPEM string, domains []string, now func() time.Time) bool {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if !cert.NotAfter.After(now()) {
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
