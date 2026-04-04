// Package scaleway implements provider.ComputeProvider and provider.DNSProvider
// against the Scaleway API. Uses UUID strings for all resource IDs.
// Security groups map to the firewall abstraction.
// VPC private networks are region-scoped; instances are zone-scoped.
package scaleway

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

const defaultBaseURL = "https://api.scaleway.com"

// Client talks to the Scaleway API.
type Client struct {
	api       *core.HTTPClient
	secretKey string
	projectID string
	zone      string // e.g. "fr-par-1"
}

// New creates a Scaleway compute client from a credentials map.
// Required: "secret_key", "project_id". Optional: "zone" (default "fr-par-1").
func New(credentials map[string]string) *Client {
	zone := credentials["zone"]
	if zone == "" {
		zone = "fr-par-1"
	}
	secretKey := credentials["secret_key"]
	return &Client{
		secretKey: secretKey,
		projectID: credentials["project_id"],
		zone:      zone,
		api: &core.HTTPClient{
			BaseURL: defaultBaseURL,
			SetAuth: func(r *http.Request) {
				r.Header.Set("X-Auth-Token", secretKey)
			},
			Label: "scaleway",
		},
	}
}

// ValidateCredentials checks that the API key is set and valid.
func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.secretKey == "" {
		return fmt.Errorf("scaleway: secret_key is required (env: SCW_SECRET_KEY)")
	}
	if c.projectID == "" {
		return fmt.Errorf("scaleway: project_id is required (env: SCW_DEFAULT_ORGANIZATION_ID)")
	}
	var result struct {
		Servers []any `json:"servers"`
	}
	if err := c.doInstance(ctx, http.MethodGet, "/servers?per_page=1", nil, &result); err != nil {
		return fmt.Errorf("scaleway: invalid credentials: %w", err)
	}
	return nil
}

// ArchForType returns the CPU architecture for a Scaleway instance type.
func (c *Client) ArchForType(instanceType string) string {
	upper := strings.ToUpper(instanceType)
	if strings.HasPrefix(upper, "AMP2") || strings.HasPrefix(upper, "COPARM1") {
		return "arm64"
	}
	return "amd64"
}

// region derives the region from the zone (e.g. "fr-par-1" → "fr-par").
func (c *Client) region() string {
	parts := strings.Split(c.zone, "-")
	if len(parts) >= 2 {
		return parts[0] + "-" + parts[1]
	}
	return c.zone
}

// instancePath returns the zone-scoped instance API path.
func (c *Client) instancePath(path string) string {
	return fmt.Sprintf("/instance/v1/zones/%s%s", c.zone, path)
}

// vpcPath returns the region-scoped VPC API path.
func (c *Client) vpcPath(path string) string {
	return fmt.Sprintf("/vpc/v2/regions/%s%s", c.region(), path)
}

// blockPath returns the zone-scoped Block Storage API path.
func (c *Client) blockPath(path string) string {
	return fmt.Sprintf("/block/v1alpha1/zones/%s%s", c.zone, path)
}

// doInstance sends a JSON request to the instance API.
func (c *Client) doInstance(ctx context.Context, method, path string, body, result any) error {
	return c.api.Do(ctx, method, c.instancePath(path), body, result)
}

// doText sends a PATCH with text/plain body (for cloud-init user data).
// Provider-specific — core.HTTPClient is JSON-only and that's correct.
func (c *Client) doText(ctx context.Context, path, text string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.api.BaseURL+c.instancePath(path), strings.NewReader(text))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	c.api.SetAuth(req)

	client := c.api.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("scaleway: set user_data: %d", resp.StatusCode)
	}
	return nil
}

// labelsToTags converts a map of labels to Scaleway's tag format: ["key=value", ...].
func labelsToTags(labels map[string]string) []string {
	tags := make([]string, 0, len(labels))
	for k, v := range labels {
		tags = append(tags, fmt.Sprintf("%s=%s", k, v))
	}
	return tags
}

// archForInstanceType returns the Scaleway arch name for image lookups.
func archForInstanceType(instanceType string) string {
	upper := strings.ToUpper(instanceType)
	if strings.HasPrefix(upper, "AMP") || strings.HasPrefix(upper, "COPARM") {
		return "arm64"
	}
	return "x86_64"
}

// volumeTypeForInstance returns the appropriate root volume type.
func volumeTypeForInstance(instanceType string) string {
	upper := strings.ToUpper(instanceType)
	if strings.HasPrefix(upper, "DEV1") || strings.HasPrefix(upper, "GP1") ||
		strings.HasPrefix(upper, "STARDUST") {
		return "l_ssd"
	}
	return "sbs_volume"
}

func (c *Client) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	var groups []provider.ResourceGroup

	servers, err := c.ListServers(ctx, nil)
	if err != nil {
		return nil, err
	}
	g := provider.ResourceGroup{Name: "Instances", Columns: []string{"ID", "Name", "Status", "IPv4", "Private IP"}}
	for _, s := range servers {
		if s.PrivateIP == "" {
			if ip, _ := c.GetPrivateIP(ctx, s.ID); ip != "" {
				s.PrivateIP = ip
			}
		}
		g.Rows = append(g.Rows, []string{s.ID, s.Name, string(s.Status), s.IPv4, s.PrivateIP})
	}
	groups = append(groups, g)

	firewalls, err := c.ListAllFirewalls(ctx)
	if err != nil {
		return nil, err
	}
	g = provider.ResourceGroup{Name: "Security Groups", Columns: []string{"ID", "Name"}}
	for _, fw := range firewalls {
		g.Rows = append(g.Rows, []string{fw.ID, fw.Name})
	}
	groups = append(groups, g)

	networks, err := c.ListAllNetworks(ctx)
	if err != nil {
		return nil, err
	}
	g = provider.ResourceGroup{Name: "Private Networks", Columns: []string{"ID", "Name"}}
	for _, n := range networks {
		g.Rows = append(g.Rows, []string{n.ID, n.Name})
	}
	groups = append(groups, g)

	volumes, err := c.ListVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}
	g = provider.ResourceGroup{Name: "Block Volumes", Columns: []string{"ID", "Name", "Size", "Zone", "Server"}}
	for _, v := range volumes {
		g.Rows = append(g.Rows, []string{v.ID, v.Name, fmt.Sprintf("%dGB", v.Size), v.Location, v.ServerID})
	}
	groups = append(groups, g)

	return groups, nil
}

var _ provider.ComputeProvider = (*Client)(nil)
