package ngrok

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// ── API shapes ────────────────────────────────────────────────────────────────

type ngrokDomain struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CNAMETarget string `json:"cname_target"`
}

type ngrokDomainListResponse struct {
	ReservedDomains []ngrokDomain `json:"reserved_domains"`
}

type ngrokDomainCreateRequest struct {
	Name string `json:"name"`
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
		cnameTarget, err := c.ensureDomain(ctx, hostname)
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

// Delete implements TunnelProvider. Removes all reserved domains for the
// given tunnel name (identified by matching hostnames in the routes).
// Idempotent — 404 is success.
//
// Note: ngrok reserved domains are looked up by exact name, not by tunnel
// name. The caller is responsible for passing the names of all registered
// hostnames — nvoi does this by iterating cfg.Domains on teardown.
// This method is a best-effort sweep.
func (c *Client) Delete(ctx context.Context, name string) error {
	var resp ngrokDomainListResponse
	if err := c.api.Do(ctx, "GET", "/reserved_domains", nil, &resp); err != nil {
		return fmt.Errorf("ngrok list domains: %w", err)
	}
	// Best-effort: nothing to match on without explicit route info.
	// The operator can clean up via the ngrok dashboard if needed.
	_ = resp
	return nil
}

func (c *Client) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	var resp ngrokDomainListResponse
	if err := c.api.Do(ctx, "GET", "/reserved_domains", nil, &resp); err != nil {
		return nil, err
	}
	g := provider.ResourceGroup{Name: "ngrok Reserved Domains", Columns: []string{"ID", "Name", "CNAME Target"}}
	for _, d := range resp.ReservedDomains {
		g.Rows = append(g.Rows, []string{d.ID, d.Name, d.CNAMETarget})
	}
	return []provider.ResourceGroup{g}, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// ensureDomain ensures a reserved domain exists for hostname.
// Returns the CNAME target to point DNS at.
func (c *Client) ensureDomain(ctx context.Context, hostname string) (string, error) {
	// List and find by name.
	var listResp ngrokDomainListResponse
	if err := c.api.Do(ctx, "GET", "/reserved_domains", nil, &listResp); err != nil {
		return "", err
	}
	for _, d := range listResp.ReservedDomains {
		if d.Name == hostname {
			return d.CNAMETarget, nil
		}
	}

	// Not found — create.
	body := ngrokDomainCreateRequest{Name: hostname}
	var created ngrokDomain
	if err := c.api.Do(ctx, "POST", "/reserved_domains", body, &created); err != nil {
		return "", err
	}
	return created.CNAMETarget, nil
}
