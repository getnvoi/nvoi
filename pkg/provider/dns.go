package provider

import "context"

// DNSProvider abstracts authoritative DNS for nvoi-managed domains.
//
// The interface is polymorphic on RECORD TYPE: the reconciler hands the
// provider an IngressBinding (DNS hint + target) and the provider picks
// the actual record kind (A / AAAA / CNAME / ALIAS / proxied-A) it
// supports best for that target. This is what lets Cloudflare flatten
// CNAME-at-apex via proxied A, lets Route53 use ALIAS records when the
// target is an AWS-native hostname, and lets Scaleway just write the
// raw A or CNAME the binding declares — all behind the same call.
//
// Three operations cover everything reconcile and the CLI need today:
// route a domain to a binding, unroute one, list everything. Each is
// idempotent. Each provider's RouteTo overwrites pre-existing records
// for the same domain — the reconciler is the source of truth.
type DNSProvider interface {
	ValidateCredentials(ctx context.Context) error

	// RouteTo makes domain resolve to binding.DNSTarget. The provider
	// picks the actual record type(s) using binding.DNSType as a hint.
	// Idempotent — repeated calls with the same binding are no-ops if
	// the records already match.
	RouteTo(ctx context.Context, domain string, binding IngressBinding) error

	// Unroute removes whatever records this provider previously created
	// for domain. Idempotent: missing records are not an error.
	Unroute(ctx context.Context, domain string) error

	// ListBindings returns every domain this provider currently serves
	// for the configured zone, with the underlying record type and
	// target value (rendered uniformly across providers).
	ListBindings(ctx context.Context) ([]DNSBinding, error)

	// ListResources returns provider-managed resources for `nvoi resources`.
	ListResources(ctx context.Context) ([]ResourceGroup, error)
}

// DNSBinding describes a live DNS record as the provider currently sees
// it. The unified shape lets the reconciler diff state without caring
// whether Cloudflare returned a proxied A or Route53 returned an ALIAS.
type DNSBinding struct {
	Domain string // FQDN
	Type   string // "A" | "AAAA" | "CNAME" | "ALIAS"
	Target string // IPv4 / IPv6 / hostname
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
