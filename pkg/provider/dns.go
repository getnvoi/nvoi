package provider

import "context"

// DNSProvider abstracts DNS record management.
// Implementations: cloudflare, hetzner (future), scaleway (future).
type DNSProvider interface {
	ValidateCredentials(ctx context.Context) error
	EnsureARecord(ctx context.Context, domain, ip string) error
	DeleteARecord(ctx context.Context, domain string) error
	ListARecords(ctx context.Context) ([]DNSRecord, error)
	ListResources(ctx context.Context) ([]ResourceGroup, error)
}

type DNSRecord struct {
	Domain string `json:"domain"`
	Type   string `json:"type"`
	IP     string `json:"ip"`
}

// RecordName extracts the short record name from a full domain relative to a zone.
// "myapp.com" with zone "myapp.com" → "@"
// "api.myapp.com" with zone "myapp.com" → "api"
func RecordName(domain, zone string) string {
	if domain == zone {
		return "@"
	}
	suffix := "." + zone
	if len(domain) > len(suffix) && domain[len(domain)-len(suffix):] == suffix {
		return domain[:len(domain)-len(suffix)]
	}
	return domain
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
