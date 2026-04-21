package ngrok

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// ── API shapes ────────────────────────────────────────────────────────────────

type ngrokDomain struct {
	ID          string `json:"id"`
	Domain      string `json:"domain"`
	CNAMETarget string `json:"cname_target"`
	Metadata    string `json:"metadata"`
}

type ngrokDomainListResponse struct {
	ReservedDomains []ngrokDomain `json:"reserved_domains"`
}

type ngrokDomainCreateRequest struct {
	Domain   string `json:"domain"`
	Metadata string `json:"metadata,omitempty"`
}

// ── TunnelProvider impl ───────────────────────────────────────────────────────

func (c *Client) ValidateCredentials(ctx context.Context) error {
	var resp ngrokDomainListResponse
	return c.api.Do(ctx, "GET", "/reserved_domains", nil, &resp)
}

// Reconcile implements TunnelProvider.
//
// For each hostname in req.Routes:
//  1. Look up existing reserved domain by name.
//  2. Create if absent.
//  3. Collect the CNAME target ngrok provides.
//
// Returns workloads (Deployment + ConfigMap + Secret) and DNS bindings.
func (c *Client) Reconcile(ctx context.Context, req provider.TunnelRequest) (*provider.TunnelPlan, error) {
	dnsBindings := make(map[string]provider.IngressBinding)

	// Collect unique hostnames from routes.
	hostnames := make(map[string]bool)
	for _, r := range req.Routes {
		hostnames[r.Hostname] = true
	}

	for hostname := range hostnames {
		cnameTarget, err := c.ensureDomain(ctx, req.Name, hostname)
		if err != nil {
			return nil, fmt.Errorf("ngrok ensure domain %s: %w", hostname, err)
		}
		dnsBindings[hostname] = provider.IngressBinding{
			DNSType:   "CNAME",
			DNSTarget: cnameTarget,
		}
	}

	workloads := BuildWorkloads(req.Name, req.Namespace, c.authtoken, req.Labels, req.Routes)

	return &provider.TunnelPlan{
		Workloads:   workloads,
		DNSBindings: dnsBindings,
	}, nil
}

// Delete implements TunnelProvider.
//
// name supports two forms:
//   - exact hostname (contains a dot) → delete that one reserved domain
//   - tunnel base name (e.g. nvoi-myapp-prod) → delete all domains carrying
//     the metadata tag this provider stamped on create
//
// Idempotent — 404 is success.
func (c *Client) Delete(ctx context.Context, name string) error {
	resp, err := c.listDomains(ctx)
	if err != nil {
		return fmt.Errorf("ngrok list domains: %w", err)
	}
	for _, d := range resp.ReservedDomains {
		switch {
		case isExactHostname(name) && d.Domain == name:
			if err := c.deleteDomain(ctx, d.ID); err != nil {
				return fmt.Errorf("ngrok delete domain %s: %w", name, err)
			}
		case !isExactHostname(name) && d.Metadata == tunnelMetadata(name):
			if err := c.deleteDomain(ctx, d.ID); err != nil {
				return fmt.Errorf("ngrok delete domain %s: %w", d.Domain, err)
			}
		}
	}
	return nil
}

func (c *Client) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	resp, err := c.listDomains(ctx)
	if err != nil {
		return nil, err
	}
	g := provider.ResourceGroup{Name: "ngrok Reserved Domains", Columns: []string{"ID", "Name", "CNAME Target"}}
	for _, d := range resp.ReservedDomains {
		g.Rows = append(g.Rows, []string{d.ID, d.Domain, d.CNAMETarget})
	}
	return []provider.ResourceGroup{g}, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// ensureDomain ensures a reserved domain exists for hostname.
// Returns the CNAME target to point DNS at.
func (c *Client) ensureDomain(ctx context.Context, tunnelName, hostname string) (string, error) {
	// List and find by name.
	listResp, err := c.listDomains(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range listResp.ReservedDomains {
		if d.Domain == hostname {
			return d.CNAMETarget, nil
		}
	}

	// Not found — create.
	body := ngrokDomainCreateRequest{
		Domain:   hostname,
		Metadata: tunnelMetadata(tunnelName),
	}
	var created ngrokDomain
	if err := c.api.Do(ctx, "POST", "/reserved_domains", body, &created); err != nil {
		return "", err
	}
	return created.CNAMETarget, nil
}

func (c *Client) listDomains(ctx context.Context) (*ngrokDomainListResponse, error) {
	var resp ngrokDomainListResponse
	if err := c.api.Do(ctx, "GET", "/reserved_domains", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) deleteDomain(ctx context.Context, id string) error {
	if err := c.api.Do(ctx, "DELETE", "/reserved_domains/"+id, nil, nil); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func tunnelMetadata(name string) string {
	return "managed-by=nvoi;tunnel=" + name
}

func isExactHostname(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool {
	type notFound interface{ HTTPStatus() int }
	if nf, ok := err.(notFound); ok {
		return nf.HTTPStatus() == 404
	}
	return false
}
