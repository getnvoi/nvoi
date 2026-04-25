package cloudflare

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// DNSClient manages A/AAAA records via the Cloudflare API.
type DNSClient struct {
	api    *utils.HTTPClient
	apiKey string
	zoneID string
	zone   string // domain name for FQDN construction
}

// ManagedByComment is the marker stamped into the Cloudflare DNS
// record's `comment` field at create/update time. Cloudflare DNS
// records carry no free-form labels, so the comment is the only
// stable provider-native ownership signal — any record carrying
// this marker was created by nvoi (RouteTo for tunnel CNAMEs and
// public-IP A records). ListResources reads it to populate the
// Owned column on `nvoi resources`.
const ManagedByComment = "managed-by:nvoi"

// NewDNS creates a Cloudflare DNS provider.
func NewDNS(creds map[string]string) *DNSClient {
	apiKey := creds["api_key"]
	return &DNSClient{
		api:    NewAPI(apiKey, "cloudflare dns"),
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

// RouteTo dispatches to A/AAAA upsert based on the binding hint.
func (d *DNSClient) RouteTo(ctx context.Context, domain string, binding provider.IngressBinding) error {
	switch binding.DNSType {
	case "A", "AAAA", "":
		// Empty DNSType: backward-compatible auto-detection from the
		// target IP literal (matches the old EnsureARecord interface).
		return d.ensureAddress(ctx, domain, binding.DNSTarget, binding.Proxied)
	case "CNAME":
		return d.ensureCNAME(ctx, domain, binding.DNSTarget, binding.Proxied)
	default:
		return fmt.Errorf("cloudflare dns: unsupported DNSType %q (want A | AAAA | CNAME)", binding.DNSType)
	}
}

// Unroute removes every A, AAAA, and CNAME record for domain. Idempotent.
func (d *DNSClient) Unroute(ctx context.Context, domain string) error {
	name := provider.RecordName(domain, d.zone)
	for _, rtype := range []string{"A", "AAAA", "CNAME"} {
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

// ensureCNAME upserts a CNAME record for domain pointing at target.
// If an existing CNAME is found it is updated in place. If conflicting
// A/AAAA records exist (e.g. left from a prior Caddy deploy) they are
// deleted first — Cloudflare rejects CNAME creation while any address
// record shares the same name.
//
// proxied controls the Cloudflare proxy flag. Tunnel deployments MUST pass
// proxied=true — cfargotunnel.com CNAMEs have no public IPs unless
// orange-clouded, causing ERR_CONNECTION_REFUSED when proxied=false.
func (d *DNSClient) ensureCNAME(ctx context.Context, domain, target string, proxied bool) error {
	name := provider.RecordName(domain, d.zone)
	fqdn := d.zone
	if name != "@" {
		fqdn = name + "." + d.zone
	}
	existing, err := d.listRecords(ctx, name, "CNAME")
	if err != nil {
		return err
	}
	for _, rec := range existing {
		if rec.Content == target && rec.Proxied == proxied && rec.Comment == ManagedByComment {
			return nil // already correct, ownership stamped
		}
		return d.updateRecord(ctx, rec.ID, cfDNSRecord{Type: "CNAME", Name: fqdn, Content: target, TTL: 300, Proxied: proxied, Comment: ManagedByComment})
	}
	// No CNAME yet — remove any A/AAAA records that would block creation.
	for _, rtype := range []string{"A", "AAAA"} {
		blocking, err := d.listRecords(ctx, name, rtype)
		if err != nil {
			return err
		}
		for _, rec := range blocking {
			if err := d.deleteRecord(ctx, rec.ID); err != nil {
				return err
			}
		}
	}
	return d.createRecord(ctx, cfDNSRecord{Type: "CNAME", Name: fqdn, Content: target, TTL: 300, Proxied: proxied, Comment: ManagedByComment})
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
// If a conflicting CNAME exists (e.g. left from a prior tunnel deploy)
// it is deleted first — Cloudflare rejects address record creation while
// a CNAME shares the same name.
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
		if rec.Content == target && rec.Proxied == proxied && rec.Comment == ManagedByComment {
			return nil // already correct, ownership stamped
		}
		return d.updateRecord(ctx, rec.ID, cfDNSRecord{Type: rtype, Name: fqdn, Content: target, TTL: 300, Proxied: proxied, Comment: ManagedByComment})
	}

	// No address record yet — remove any CNAME that would block creation.
	blocking, err := d.listRecords(ctx, name, "CNAME")
	if err != nil {
		return err
	}
	for _, rec := range blocking {
		if err := d.deleteRecord(ctx, rec.ID); err != nil {
			return err
		}
	}

	return d.createRecord(ctx, cfDNSRecord{Type: rtype, Name: fqdn, Content: target, TTL: 300, Proxied: proxied, Comment: ManagedByComment})
}

// ── API types ────────────────────────────────────────────────────────────────

type cfDNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied bool   `json:"proxied"`
	// Comment carries ManagedByComment on records nvoi creates or
	// updates. Cloudflare ignores the value semantically; we use it
	// as the ownership marker for `nvoi resources`.
	Comment string `json:"comment,omitempty"`
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

// ListResources lists every record in the zone with type A, AAAA, or
// CNAME — the three nvoi creates. CNAMEs were dropped by the previous
// implementation (it delegated to ListBindings, which is A/AAAA-only),
// hiding tunnel records like `api.nvoi.to` from `nvoi resources`. The
// Owned column reads the record `comment` against ManagedByComment;
// pre-existing manual records and prior nvoi records that pre-date the
// comment stamp surface as `Owned=false` until the next deploy
// re-stamps them.
func (d *DNSClient) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	g := provider.ResourceGroup{Name: "DNS Records", Columns: []string{"Type", "Domain", "Target"}}
	for _, rtype := range []string{"A", "AAAA", "CNAME"} {
		var resp struct {
			Result []cfDNSRecord `json:"result"`
		}
		if err := d.api.Do(ctx, "GET", fmt.Sprintf("/zones/%s/dns_records?type=%s&per_page=1000", d.zoneID, rtype), nil, &resp); err != nil {
			return nil, err
		}
		for _, rec := range resp.Result {
			g.Rows = append(g.Rows, []string{rec.Type, rec.Name, rec.Content})
			g.Owned = append(g.Owned, rec.Comment == ManagedByComment)
		}
	}
	return []provider.ResourceGroup{g}, nil
}

var _ provider.DNSProvider = (*DNSClient)(nil)
