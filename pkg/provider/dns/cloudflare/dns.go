package cloudflare

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/cfbase"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// DNSClient manages A/AAAA records via the Cloudflare API.
type DNSClient struct {
	api    *utils.HTTPClient
	apiKey string
	zoneID string
	zone   string // domain name for FQDN construction
}

// NewDNS creates a Cloudflare DNS provider.
func NewDNS(creds map[string]string) *DNSClient {
	apiKey := creds["api_key"]
	return &DNSClient{
		api:    cfbase.NewAPI(apiKey, "cloudflare dns"),
		apiKey: apiKey,
		zoneID: creds["zone_id"],
		zone:   creds["zone"],
	}
}

// APIClient returns the underlying HTTP client for tests to override BaseURL.
// Production callers must not depend on this accessor.
func (d *DNSClient) APIClient() *utils.HTTPClient { return d.api }

func (d *DNSClient) ValidateCredentials(ctx context.Context) error {
	if d.zoneID == "" {
		return fmt.Errorf("cloudflare dns: zone_id is required")
	}
	_, err := d.listRecords(ctx, "@", "A")
	if err != nil {
		return fmt.Errorf("cloudflare dns: %w", err)
	}
	return nil
}

// RouteTo dispatches to A/AAAA upsert based on the binding hint. CNAME
// targets are reserved for the managed-k8s / tunnel-provider work
// (tracked in #48 and #49) and rejected here in v1 — IaaS providers all
// emit IngressBinding{DNSType:"A"}.
func (d *DNSClient) RouteTo(ctx context.Context, domain string, binding provider.IngressBinding) error {
	switch binding.DNSType {
	case "A", "AAAA", "":
		// Empty DNSType: backward-compatible auto-detection from the
		// target IP literal (matches the old EnsureARecord interface).
		return d.ensureAddress(ctx, domain, binding.DNSTarget, false)
	case "CNAME":
		return fmt.Errorf("cloudflare dns: CNAME target %q for %s not supported in v1 — tracked in #48 (managed-k8s) / #49 (tunnel providers)", binding.DNSTarget, domain)
	default:
		return fmt.Errorf("cloudflare dns: unsupported DNSType %q (want A | AAAA | CNAME)", binding.DNSType)
	}
}

// Unroute removes every A and AAAA record for domain. Idempotent.
func (d *DNSClient) Unroute(ctx context.Context, domain string) error {
	name := provider.RecordName(domain, d.zone)
	for _, rtype := range []string{"A", "AAAA"} {
		records, err := d.listRecords(ctx, name, rtype)
		if err != nil {
			return err
		}
		for _, rec := range records {
			if err := d.deleteRecord(ctx, rec.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ListBindings returns every A/AAAA record currently in the zone.
func (d *DNSClient) ListBindings(ctx context.Context) ([]provider.DNSBinding, error) {
	var bindings []provider.DNSBinding
	for _, rtype := range []string{"A", "AAAA"} {
		var resp struct {
			Result []cfDNSRecord `json:"result"`
		}
		if err := d.api.Do(ctx, "GET", fmt.Sprintf("/zones/%s/dns_records?type=%s&per_page=1000", d.zoneID, rtype), nil, &resp); err != nil {
			return nil, err
		}
		for _, rec := range resp.Result {
			bindings = append(bindings, provider.DNSBinding{Domain: rec.Name, Type: rec.Type, Target: rec.Content})
		}
	}
	return bindings, nil
}

// ensureAddress is the internal A/AAAA upsert used by RouteTo. The
// record type is auto-detected from target (RecordType returns "AAAA"
// for any literal containing ":", "A" otherwise).
func (d *DNSClient) ensureAddress(ctx context.Context, domain, target string, proxied bool) error {
	rtype := provider.RecordType(target)
	name := provider.RecordName(domain, d.zone)

	fqdn := d.zone
	if name != "@" {
		fqdn = name + "." + d.zone
	}

	existing, err := d.listRecords(ctx, name, rtype)
	if err != nil {
		return err
	}

	for _, rec := range existing {
		if rec.Content == target && rec.Proxied == proxied {
			return nil // already correct
		}
		return d.updateRecord(ctx, rec.ID, cfDNSRecord{Type: rtype, Name: fqdn, Content: target, TTL: 300, Proxied: proxied})
	}

	return d.createRecord(ctx, cfDNSRecord{Type: rtype, Name: fqdn, Content: target, TTL: 300, Proxied: proxied})
}

// ── API types ────────────────────────────────────────────────────────────────

type cfDNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied bool   `json:"proxied"`
}

// ── API calls ────────────────────────────────────────────────────────────────

func (d *DNSClient) listRecords(ctx context.Context, name, rtype string) ([]cfDNSRecord, error) {
	fqdn := d.zone
	if name != "@" && name != "" {
		fqdn = name + "." + d.zone
	}
	var resp struct {
		Result []cfDNSRecord `json:"result"`
	}
	if err := d.api.Do(ctx, "GET", fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", d.zoneID, rtype, fqdn), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func (d *DNSClient) createRecord(ctx context.Context, r cfDNSRecord) error {
	return d.api.Do(ctx, "POST", fmt.Sprintf("/zones/%s/dns_records", d.zoneID), r, nil)
}

func (d *DNSClient) updateRecord(ctx context.Context, id string, r cfDNSRecord) error {
	return d.api.Do(ctx, "PUT", fmt.Sprintf("/zones/%s/dns_records/%s", d.zoneID, id), r, nil)
}

func (d *DNSClient) deleteRecord(ctx context.Context, id string) error {
	return d.api.Do(ctx, "DELETE", fmt.Sprintf("/zones/%s/dns_records/%s", d.zoneID, id), nil, nil)
}

func (d *DNSClient) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	bindings, err := d.ListBindings(ctx)
	if err != nil {
		return nil, err
	}
	g := provider.ResourceGroup{Name: "DNS Records", Columns: []string{"Type", "Domain", "Target"}}
	for _, b := range bindings {
		g.Rows = append(g.Rows, []string{b.Type, b.Domain, b.Target})
	}
	return []provider.ResourceGroup{g}, nil
}

var _ provider.DNSProvider = (*DNSClient)(nil)
