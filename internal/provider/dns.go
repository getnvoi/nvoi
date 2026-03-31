package provider

import "context"

// DNSProvider abstracts DNS record management.
// Implementations: cloudflare, hetzner (future), scaleway (future).
type DNSProvider interface {
	ValidateCredentials(ctx context.Context) error
	EnsureARecord(ctx context.Context, domain, ip string) error
	DeleteARecord(ctx context.Context, domain string) error
	ListARecords(ctx context.Context) ([]DNSRecord, error)
}

type DNSRecord struct {
	Domain string
	Type   string // "A" or "AAAA"
	IP     string
}

// RecordType returns "A" for IPv4, "AAAA" for IPv6.
func RecordType(ip string) string {
	for i := 0; i < len(ip); i++ {
		if ip[i] == ':' {
			return "AAAA"
		}
	}
	return "A"
}
