package scaleway

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── API payloads ────────────────────────────────────────────────────────────────

type serverCreateBody struct {
	Name              string                  `json:"name"`
	CommercialType    string                  `json:"commercial_type"`
	Image             string                  `json:"image"`
	Project           string                  `json:"project"`
	Tags              []string                `json:"tags,omitempty"`
	Volumes           map[string]serverVolume `json:"volumes,omitempty"`
	SecurityGroup     string                  `json:"security_group,omitempty"`
	DynamicIPRequired bool                    `json:"dynamic_ip_required"`
}

type serverVolume struct {
	Size       int64  `json:"size"`
	VolumeType string `json:"volume_type"`
}

type serverJSON struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	State       string       `json:"state"`
	PublicIP    *publicIP    `json:"public_ip"`
	PublicIPs   []publicIP   `json:"public_ips"`
	PrivateNics []privateNic `json:"private_nics"`
	Tags        []string     `json:"tags"`
}

type publicIP struct {
	Address string `json:"address"`
}

type privateNic struct {
	ID          string   `json:"id"`
	IPAddresses []string `json:"ip_addresses"`
}

// ── Converters ──────────────────────────────────────────────────────────────────

var uuidRe = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

var imageAliases = map[string]string{
	"ubuntu-24.04": "ubuntu_noble",
	"ubuntu-22.04": "ubuntu_jammy",
	"ubuntu-20.04": "ubuntu_focal",
}

func serverFromJSON(s serverJSON) *provider.Server {
	srv := &provider.Server{
		ID:     s.ID,
		Name:   s.Name,
		Status: provider.ServerStatus(s.State),
	}
	if s.PublicIP != nil && s.PublicIP.Address != "" {
		srv.IPv4 = s.PublicIP.Address
	} else if len(s.PublicIPs) > 0 {
		srv.IPv4 = s.PublicIPs[0].Address
	}
	if len(s.PrivateNics) > 0 && len(s.PrivateNics[0].IPAddresses) > 0 {
		srv.PrivateIP = s.PrivateNics[0].IPAddresses[0]
	}
	return srv
}

// ── Interface methods ───────────────────────────────────────────────────────────

func (c *Client) EnsureServer(ctx context.Context, req provider.CreateServerRequest) (*provider.Server, error) {
	existing, err := c.getServerByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		// Reconcile: attach private network if missing.
		if req.NetworkName != "" {
			net, err := c.getNetworkByName(ctx, req.NetworkName)
			if err == nil && net != nil {
				privateIP, _ := c.getPrivateIP(ctx, existing.ID)
				if privateIP == "" {
					_ = c.attachPrivateNetwork(ctx, existing.ID, net.ID)
				}
			}
		}
		return existing, nil
	}

	return c.createServer(ctx, req)
}

func (c *Client) createServer(ctx context.Context, req provider.CreateServerRequest) (*provider.Server, error) {
	// Resolve firewall (security group)
	fwID, err := c.ensureFirewall(ctx, req.FirewallName, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("firewall: %w", err)
	}

	// Resolve network
	netID, err := c.ensureNetwork(ctx, req.NetworkName, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}

	// Resolve image
	imageID, err := c.resolveImage(ctx, req.Image, req.ServerType)
	if err != nil {
		return nil, fmt.Errorf("resolve image: %w", err)
	}

	body := serverCreateBody{
		Name:              req.Name,
		CommercialType:    req.ServerType,
		Image:             imageID,
		Project:           c.projectID,
		Tags:              labelsToTags(req.Labels),
		DynamicIPRequired: true,
		Volumes: map[string]serverVolume{
			"0": {Size: 20_000_000_000, VolumeType: volumeTypeForInstance(req.ServerType)},
		},
	}
	if fwID != "" {
		body.SecurityGroup = fwID
	}

	var resp struct {
		Server serverJSON `json:"server"`
	}
	if err := c.doInstance(ctx, "POST", "/servers", body, &resp); err != nil {
		return nil, fmt.Errorf("create server: %w", err)
	}

	serverID := resp.Server.ID

	// Set cloud-init user data
	if req.UserData != "" {
		if err := c.doText(ctx, fmt.Sprintf("/servers/%s/user_data/cloud-init", serverID), req.UserData); err != nil {
			return nil, fmt.Errorf("set user_data: %w", err)
		}
	}

	// Attach private network (before power-on)
	if netID != "" {
		if err := c.attachPrivateNetwork(ctx, serverID, netID); err != nil {
			return nil, fmt.Errorf("attach network: %w", err)
		}
	}

	// Power on
	if err := c.doInstance(ctx, "POST", fmt.Sprintf("/servers/%s/action", serverID), map[string]string{"action": "poweron"}, nil); err != nil {
		return nil, fmt.Errorf("power on: %w", err)
	}

	// Re-fetch for assigned IPs
	var srvResp struct {
		Server serverJSON `json:"server"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/servers/%s", serverID), nil, &srvResp); err != nil {
		return nil, fmt.Errorf("refetch server: %w", err)
	}
	return serverFromJSON(srvResp.Server), nil
}

func (c *Client) DeleteServer(ctx context.Context, req provider.DeleteServerRequest) error {
	srv, err := c.getServerByName(ctx, req.Name)
	if err != nil {
		return err
	}
	if srv == nil {
		return utils.ErrNotFound
	}

	// Terminate (async)
	err = c.doInstance(ctx, "POST", fmt.Sprintf("/servers/%s/action", srv.ID), map[string]string{"action": "terminate"}, nil)
	if err != nil {
		// Fallback to direct delete
		if delErr := c.doInstance(ctx, "DELETE", fmt.Sprintf("/servers/%s", srv.ID), nil, nil); delErr != nil {
			return fmt.Errorf("delete server: %w (terminate: %v)", delErr, err)
		}
	}

	// Poll until gone
	if err := utils.Poll(ctx, 3*time.Second, 90*time.Second, func() (bool, error) {
		s, err := c.getServerByName(ctx, req.Name)
		return s == nil || err != nil, nil
	}); err != nil {
		return fmt.Errorf("server %s did not terminate: %w", req.Name, err)
	}

	return nil
}

func (c *Client) ListServers(ctx context.Context, labels map[string]string) ([]*provider.Server, error) {
	tags := labelsToTags(labels)
	tagParam := ""
	if len(tags) > 0 {
		tagParam = "&tags=" + strings.Join(tags, ",")
	}

	var resp struct {
		Servers []serverJSON `json:"servers"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/servers?project=%s&per_page=50%s", c.projectID, tagParam), nil, &resp); err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}

	servers := make([]*provider.Server, 0, len(resp.Servers))
	for _, s := range resp.Servers {
		servers = append(servers, serverFromJSON(s))
	}
	return servers, nil
}

// ── Internal helpers ────────────────────────────────────────────────────────────

func (c *Client) getServerByName(ctx context.Context, name string) (*provider.Server, error) {
	var resp struct {
		Servers []serverJSON `json:"servers"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/servers?name=%s&project=%s", name, c.projectID), nil, &resp); err != nil {
		return nil, fmt.Errorf("get server by name: %w", err)
	}
	for _, s := range resp.Servers {
		if s.Name == name {
			return serverFromJSON(s), nil
		}
	}
	return nil, nil
}

func (c *Client) GetPrivateIP(ctx context.Context, serverID string) (string, error) {
	return c.getPrivateIP(ctx, serverID)
}

func (c *Client) getPrivateIP(ctx context.Context, serverID string) (string, error) {
	var nicsResp struct {
		PrivateNics []struct {
			ID string `json:"id"`
		} `json:"private_nics"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/servers/%s/private_nics", serverID), nil, &nicsResp); err != nil {
		return "", err
	}
	if len(nicsResp.PrivateNics) == 0 {
		return "", nil
	}

	nicID := nicsResp.PrivateNics[0].ID
	var ipamResp struct {
		IPs []struct {
			Address string `json:"address"`
		} `json:"ips"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/ipam/v1/regions/%s/ips?resource_id=%s&is_ipv6=false", c.region(), nicID), nil, &ipamResp); err != nil {
		return "", err
	}

	for _, ip := range ipamResp.IPs {
		addr := ip.Address
		if idx := strings.Index(addr, "/"); idx >= 0 {
			addr = addr[:idx]
		}
		if strings.HasPrefix(addr, "10.") {
			return addr, nil
		}
	}
	return "", nil
}

func (c *Client) resolveImage(ctx context.Context, image, instanceType string) (string, error) {
	if uuidRe.MatchString(image) {
		return image, nil
	}
	if alias, ok := imageAliases[image]; ok {
		image = alias
	}
	arch := archForInstanceType(instanceType)
	var resp struct {
		Images []struct {
			ID string `json:"id"`
		} `json:"images"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/images?arch=%s&name=%s", arch, image), nil, &resp); err != nil {
		return "", fmt.Errorf("lookup image %q: %w", image, err)
	}
	if len(resp.Images) == 0 {
		return "", fmt.Errorf("image %q not found for arch %s in zone %s", image, arch, c.zone)
	}
	return resp.Images[0].ID, nil
}
