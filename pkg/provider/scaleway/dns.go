package scaleway

import (
	"context"
	"fmt"
	"net/http"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// DNSClient manages A/AAAA records via the Scaleway DNS API (v2beta1).
// Uses PATCH-based change API for atomic record operations.
type DNSClient struct {
	api  *utils.HTTPClient
	zone string
}

// NewDNS creates a Scaleway DNS provider.
func NewDNS(creds map[string]string) *DNSClient {
	return &DNSClient{
		api: &utils.HTTPClient{
			BaseURL: "https://api.scaleway.com/domain/v2beta1",
			SetAuth: func(r *http.Request) {
				r.Header.Set("X-Auth-Token", creds["secret_key"])
			},
			Label: "scaleway dns",
		},
		zone: creds["zone"],
	}
}

func (d *DNSClient) ValidateCredentials(ctx context.Context) error {
	if d.zone == "" {
		return fmt.Errorf("scaleway dns: zone is required")
	}
	_, err := d.listRecords(ctx, "@", "A")
	if err != nil {
		return fmt.Errorf("scaleway dns: %w", err)
	}
	return nil
}

func (d *DNSClient) EnsureARecord(ctx context.Context, domain, ip string, proxied bool) error {
	name := provider.RecordName(domain, d.zone)
	rtype := provider.RecordType(ip)

	return d.patchRecords(ctx, patchRequest{
		ReturnAllRecords: false,
		Changes: []change{{
			Set: &changeSet{
				Name: name,
				Type: rtype,
				Records: []recordData{{
					Name: name,
					Data: ip,
					Type: rtype,
					TTL:  300,
				}},
			},
		}},
	})
}

func (d *DNSClient) DeleteARecord(ctx context.Context, domain string) error {
	name := provider.RecordName(domain, d.zone)
	var changes []change
	for _, rtype := range []string{"A", "AAAA"} {
		changes = append(changes, change{
			Delete: &changeDelete{Name: name, Type: rtype},
		})
	}
	return d.patchRecords(ctx, patchRequest{
		ReturnAllRecords: false,
		Changes:          changes,
	})
}

func (d *DNSClient) ListARecords(ctx context.Context) ([]provider.DNSRecord, error) {
	var out []provider.DNSRecord
	for _, rtype := range []string{"A", "AAAA"} {
		records, err := d.listRecords(ctx, "", rtype)
		if err != nil {
			return nil, err
		}
		for _, r := range records {
			domain := d.zone
			if r.Name != "" && r.Name != "@" {
				domain = r.Name + "." + d.zone
			}
			out = append(out, provider.DNSRecord{Domain: domain, IP: r.Data, Type: r.Type})
		}
	}
	return out, nil
}

// ── API types ────────────────────────────────────────────────────────────────────

type recordData struct {
	Name string `json:"name"`
	Data string `json:"data"`
	Type string `json:"type"`
	TTL  int    `json:"ttl"`
}

type patchRequest struct {
	ReturnAllRecords bool     `json:"return_all_records"`
	Changes          []change `json:"changes"`
}

type change struct {
	Set    *changeSet    `json:"set,omitempty"`
	Delete *changeDelete `json:"delete,omitempty"`
}

type changeSet struct {
	Name    string       `json:"name"`
	Type    string       `json:"type"`
	Records []recordData `json:"records"`
}

type changeDelete struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ── API calls ────────────────────────────────────────────────────────────────────

func (d *DNSClient) listRecords(ctx context.Context, name, rtype string) ([]recordData, error) {
	path := fmt.Sprintf("/dns-zones/%s/records?page_size=100", d.zone)
	if name != "" {
		path += "&name=" + name
	}
	if rtype != "" {
		path += "&type=" + rtype
	}
	var resp struct {
		Records []recordData `json:"records"`
	}
	if err := d.api.Do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Records, nil
}

func (d *DNSClient) patchRecords(ctx context.Context, req patchRequest) error {
	return d.api.Do(ctx, "PATCH", fmt.Sprintf("/dns-zones/%s/records", d.zone), req, nil)
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
