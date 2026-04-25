package cloudflare

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── API shapes ────────────────────────────────────────────────────────────────

type cfTunnel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type cfTunnelListResponse struct {
	Result []cfTunnel `json:"result"`
}

type cfTunnelCreateRequest struct {
	Name      string `json:"name"`
	ConfigSrc string `json:"config_src"`
}

type cfTunnelCreateResponse struct {
	Result cfTunnel `json:"result"`
}

type cfTunnelTokenResponse struct {
	Result string `json:"result"`
}

type cfIngressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
}

type cfTunnelConfig struct {
	Ingress []cfIngressRule `json:"ingress"`
}

type cfTunnelConfigRequest struct {
	Config cfTunnelConfig `json:"config"`
}

// ── TunnelProvider impl ───────────────────────────────────────────────────────

func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.accountID == "" {
		return fmt.Errorf("cloudflare tunnel: account_id is required")
	}
	// Light probe: list tunnels (page 1, limit 1).
	var resp cfTunnelListResponse
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel?is_deleted=false&per_page=1", c.accountID)
	return c.api.Do(ctx, "GET", path, nil, &resp)
}

// Reconcile implements TunnelProvider.
//
// Full sequence per the issue spec:
//  1. GET cfd_tunnel?name=...&is_deleted=false — lookup by deterministic name
//  2. POST cfd_tunnel — create only if lookup returned empty
//  3. GET cfd_tunnel/{id}/token — fetch agent token
//  4. PUT cfd_tunnel/{id}/configurations — push routing table
func (c *Client) Reconcile(ctx context.Context, req provider.TunnelRequest) (*provider.TunnelPlan, error) {
	// 1. Lookup — is_deleted=false is mandatory to exclude soft-deleted tombstones.
	tunnelID, err := c.findTunnel(ctx, req.Name)
	if err != nil {
		return nil, fmt.Errorf("cloudflare tunnel find: %w", err)
	}

	// 2. Create if absent.
	if tunnelID == "" {
		id, err := c.createTunnel(ctx, req.Name)
		if err != nil {
			return nil, fmt.Errorf("cloudflare tunnel create: %w", err)
		}
		tunnelID = id
	}

	// 3. Fetch the agent token.
	token, err := c.fetchToken(ctx, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("cloudflare tunnel token: %w", err)
	}

	// 4. Push the full routing table. Last rule: http_status:404 catch-all.
	if err := c.pushConfig(ctx, tunnelID, req.Routes); err != nil {
		return nil, fmt.Errorf("cloudflare tunnel config: %w", err)
	}

	// Build the TunnelPlan.
	workloads := BuildWorkloads(req.Name, tunnelID, token, req.Labels)

	dnsBindings := make(map[string]provider.IngressBinding, len(req.Routes))
	cnameTarget := fmt.Sprintf("%s.cfargotunnel.com", tunnelID)
	for _, r := range req.Routes {
		dnsBindings[r.Hostname] = provider.IngressBinding{
			DNSType:   "CNAME",
			DNSTarget: cnameTarget,
			// Proxied MUST be true: cfargotunnel.com has no public IPs unless
			// the record is orange-clouded. Without it the domain is
			// unresolvable and all requests get ERR_CONNECTION_REFUSED.
			Proxied: true,
		}
	}

	return &provider.TunnelPlan{
		Workloads:   workloads,
		DNSBindings: dnsBindings,
	}, nil
}

// Delete implements TunnelProvider. Idempotent — 404 is success.
// Cloudflare exposes connector teardown separately from tunnel deletion,
// so remove active connections first and then delete the tunnel itself.
func (c *Client) Delete(ctx context.Context, name string) error {
	tunnelID, err := c.findTunnel(ctx, name)
	if err != nil {
		return fmt.Errorf("cloudflare tunnel find: %w", err)
	}
	if tunnelID == "" {
		return nil // already gone
	}
	if err := c.deleteConnections(ctx, tunnelID); err != nil && !utils.IsNotFound(err) {
		return fmt.Errorf("cloudflare tunnel connections delete: %w", err)
	}
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", c.accountID, tunnelID)
	if err := c.api.Do(ctx, "DELETE", path, nil, nil); err != nil {
		if utils.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("cloudflare tunnel delete: %w", err)
	}
	return nil
}

// ListResources lists every active (non-soft-deleted) Cloudflare
// Tunnel on the account. Cloudflare Tunnels carry no free-form labels
// or comments via the API, so Owned is computed from the deterministic
// `nvoi-` name prefix Reconcile stamps on creation. Tunnels named
// outside that pattern surface with Owned=false.
func (c *Client) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	var resp cfTunnelListResponse
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel?is_deleted=false", c.accountID)
	if err := c.api.Do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	g := provider.ResourceGroup{Name: "Cloudflare Tunnels", Columns: []string{"ID", "Name", "Status"}}
	for _, t := range resp.Result {
		g.Rows = append(g.Rows, []string{t.ID, t.Name, t.Status})
		g.Owned = append(g.Owned, strings.HasPrefix(t.Name, "nvoi-"))
	}
	return []provider.ResourceGroup{g}, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// findTunnel looks up a tunnel by exact name, excluding soft-deleted ones.
// is_deleted=false is mandatory — omitting it returns tombstones from prior
// deploys which would poison the reuse path.
func (c *Client) findTunnel(ctx context.Context, name string) (string, error) {
	var resp cfTunnelListResponse
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel?name=%s&is_deleted=false", c.accountID, name)
	if err := c.api.Do(ctx, "GET", path, nil, &resp); err != nil {
		return "", err
	}
	for _, t := range resp.Result {
		if t.Name == name {
			return t.ID, nil
		}
	}
	return "", nil
}

func (c *Client) createTunnel(ctx context.Context, name string) (string, error) {
	body := cfTunnelCreateRequest{Name: name, ConfigSrc: "cloudflare"}
	var resp cfTunnelCreateResponse
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel", c.accountID)
	if err := c.api.Do(ctx, "POST", path, body, &resp); err != nil {
		return "", err
	}
	return resp.Result.ID, nil
}

func (c *Client) fetchToken(ctx context.Context, tunnelID string) (string, error) {
	var resp cfTunnelTokenResponse
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/token", c.accountID, tunnelID)
	if err := c.api.Do(ctx, "GET", path, nil, &resp); err != nil {
		return "", err
	}
	return resp.Result, nil
}

func (c *Client) pushConfig(ctx context.Context, tunnelID string, routes []provider.TunnelRoute) error {
	ingress := make([]cfIngressRule, 0, len(routes)+1)
	for _, r := range routes {
		scheme := r.Scheme
		if scheme == "" {
			scheme = "http"
		}
		svc := fmt.Sprintf("%s://%s:%d", scheme, r.ServiceName, r.ServicePort)
		ingress = append(ingress, cfIngressRule{Hostname: r.Hostname, Service: svc})
	}
	// Mandatory catch-all: unmatched hostnames → 404 at CF edge.
	ingress = append(ingress, cfIngressRule{Service: "http_status:404"})

	body := cfTunnelConfigRequest{Config: cfTunnelConfig{Ingress: ingress}}
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/configurations", c.accountID, tunnelID)
	return c.api.Do(ctx, "PUT", path, body, nil)
}

func (c *Client) deleteConnections(ctx context.Context, tunnelID string) error {
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/connections", c.accountID, tunnelID)
	return c.api.Do(ctx, "DELETE", path, nil, nil)
}
