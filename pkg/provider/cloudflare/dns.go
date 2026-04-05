package cloudflare

import (
	"context"
	"fmt"
	"net/http"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// DNSClient manages A/AAAA records via the Cloudflare API.
type DNSClient struct {
	api    *utils.HTTPClient
	zoneID string
	zone   string // domain name for FQDN construction
}

// NewDNS creates a Cloudflare DNS provider.
func NewDNS(creds map[string]string) *DNSClient {
	return &DNSClient{
		api: &utils.HTTPClient{
			BaseURL: cfBaseURL,
			SetAuth: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+creds["api_key"])
			},
			Label: "cloudflare dns",
		},
		zoneID: creds["zone_id"],
		zone:   creds["zone"],
	}
}

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

func (d *DNSClient) EnsureARecord(ctx context.Context, domain, ip string) error {
	rtype := provider.RecordType(ip)
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
		if rec.Content == ip && !rec.Proxied {
			return nil // already correct
		}
		return d.updateRecord(ctx, rec.ID, cfDNSRecord{Type: rtype, Name: fqdn, Content: ip, TTL: 300, Proxied: false})
	}

	return d.createRecord(ctx, cfDNSRecord{Type: rtype, Name: fqdn, Content: ip, TTL: 300, Proxied: false})
}

func (d *DNSClient) DeleteARecord(ctx context.Context, domain string) error {
	name := provider.RecordName(domain, d.zone)
	found := false
	for _, rtype := range []string{"A", "AAAA"} {
		records, err := d.listRecords(ctx, name, rtype)
		if err != nil {
			return err
		}
		for _, rec := range records {
			found = true
			if err := d.deleteRecord(ctx, rec.ID); err != nil {
				return err
			}
		}
	}
	if !found {
		return utils.ErrNotFound
	}
	return nil
}

func (d *DNSClient) ListARecords(ctx context.Context) ([]provider.DNSRecord, error) {
	var records []provider.DNSRecord
	for _, rtype := range []string{"A", "AAAA"} {
		var resp struct {
			Result []cfDNSRecord `json:"result"`
		}
		if err := d.api.Do(ctx, "GET", fmt.Sprintf("/zones/%s/dns_records?type=%s&per_page=1000", d.zoneID, rtype), nil, &resp); err != nil {
			return nil, err
		}
		for _, rec := range resp.Result {
			records = append(records, provider.DNSRecord{Domain: rec.Name, IP: rec.Content, Type: rec.Type})
		}
	}
	return records, nil
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
	records, err := d.ListARecords(ctx)
	if err != nil {
		return nil, err
	}
	g := provider.ResourceGroup{Name: "DNS Records", Columns: []string{"Type", "Domain", "IP"}}
	for _, r := range records {
		g.Rows = append(g.Rows, []string{r.Type, r.Domain, r.IP})
	}
	return []provider.ResourceGroup{g}, nil
}

var _ provider.DNSProvider = (*DNSClient)(nil)
