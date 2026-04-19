package scaleway

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// DNSClient manages A/AAAA records via the Scaleway DNS API (v2beta1).
// Uses PATCH-based change API for atomic record operations.
type DNSClient struct {
	api  *utils.HTTPClient
	zone string
}

// NewDNS creates a Scaleway DNS provider.
func NewDNS(creds map[string]string) *DNSClient {
	api := NewAPI(creds["secret_key"], "scaleway dns")
	api.BaseURL = BaseURL + "/domain/v2beta1"
	return &DNSClient{
		api:  api,
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

// RouteTo dispatches to A/AAAA/CNAME upsert based on the binding hint.
func (d *DNSClient) RouteTo(ctx context.Context, domain string, binding provider.IngressBinding) error {
	switch binding.DNSType {
	case "A", "AAAA", "":
		return d.ensureAddress(ctx, domain, binding.DNSTarget)
	case "CNAME":
		return d.ensureCNAME(ctx, domain, binding.DNSTarget)
	default:
		return fmt.Errorf("scaleway dns: unsupported DNSType %q (want A | AAAA | CNAME)", binding.DNSType)
	}
}

// Unroute removes every A/AAAA/CNAME record for domain. Idempotent — the
// Scaleway PATCH API tolerates deletes against absent records.
func (d *DNSClient) Unroute(ctx context.Context, domain string) error {
	name := provider.RecordName(domain, d.zone)
	var changes []change
	for _, rtype := range []string{"A", "AAAA", "CNAME"} {
		changes = append(changes, change{
			Delete: &changeDelete{Name: name, Type: rtype},
		})
	}
	return d.patchRecords(ctx, patchRequest{
		ReturnAllRecords: false,
		Changes:          changes,
	})
}

// ensureCNAME upserts a CNAME record for domain pointing at target.
func (d *DNSClient) ensureCNAME(ctx context.Context, domain, target string) error {
	name := provider.RecordName(domain, d.zone)

	return d.patchRecords(ctx, patchRequest{
		ReturnAllRecords: false,
		Changes: []change{
			{Delete: &changeDelete{Name: name, Type: "A"}},
			{Delete: &changeDelete{Name: name, Type: "AAAA"}},
			{
				Set: &changeSet{
					Name: name,
					Type: "CNAME",
					Records: []recordData{{
						Name: name,
						Data: target,
						Type: "CNAME",
						TTL:  300,
					}},
				},
			},
		},
	})
}

// ListBindings returns every A/AAAA record in the configured zone.
func (d *DNSClient) ListBindings(ctx context.Context) ([]provider.DNSBinding, error) {
	var out []provider.DNSBinding
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
			out = append(out, provider.DNSBinding{Domain: domain, Target: r.Data, Type: r.Type})
		}
	}
	return out, nil
}

// ensureAddress is the internal A/AAAA upsert used by RouteTo. Record
// type auto-detected from target IP literal.
func (d *DNSClient) ensureAddress(ctx context.Context, domain, target string) error {
	name := provider.RecordName(domain, d.zone)
	rtype := provider.RecordType(target)

	return d.patchRecords(ctx, patchRequest{
		ReturnAllRecords: false,
		Changes: []change{
			{Delete: &changeDelete{Name: name, Type: "CNAME"}},
			{
				Set: &changeSet{
					Name: name,
					Type: rtype,
					Records: []recordData{{
						Name: name,
						Data: target,
						Type: rtype,
						TTL:  300,
					}},
				},
			},
		},
	})
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
