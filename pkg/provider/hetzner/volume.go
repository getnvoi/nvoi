package hetzner

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

type volumeJSON struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Size        int               `json:"size"`
	Server      *int64            `json:"server"`
	Location    struct{ Name string `json:"name"` } `json:"location"`
	Labels      map[string]string `json:"labels"`
	LinuxDevice string            `json:"linux_device"`
	Status      string            `json:"status"`
}

func volumeFrom(v volumeJSON) *provider.Volume {
	vol := &provider.Volume{
		ID:         strconv.FormatInt(v.ID, 10),
		Name:       v.Name,
		Size:       v.Size,
		DevicePath: v.LinuxDevice,
		Location:   v.Location.Name,
	}
	if v.Server != nil {
		vol.ServerID = strconv.FormatInt(*v.Server, 10)
	}
	return vol
}

// EnsureVolume creates or finds a volume by name, attaches it to the named server.
// Idempotent: if volume exists and is attached to the right server, no-op.
// Raises if server doesn't exist. Raises if size doesn't match existing volume.
func (c *Client) EnsureVolume(ctx context.Context, req provider.CreateVolumeRequest) (*provider.Volume, error) {
	if req.Size <= 0 {
		return nil, fmt.Errorf("volume size must be > 0 (got %d)", req.Size)
	}

	// Resolve server by name
	srv, err := c.getServerByName(ctx, req.ServerName)
	if err != nil {
		return nil, fmt.Errorf("resolve server %s: %w", req.ServerName, err)
	}
	if srv == nil {
		return nil, fmt.Errorf("server %s not found — run 'compute set' first", req.ServerName)
	}

	// Find existing volume by name
	vol, err := c.getVolumeByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}

	if vol != nil {
		if req.Size < vol.Size {
			return nil, fmt.Errorf("volume %s is %dGB, requested %dGB — shrinking not supported", req.Name, vol.Size, req.Size)
		}
		if req.Size > vol.Size {
			if err := c.ResizeVolume(ctx, vol.ID, req.Size); err != nil {
				return nil, err
			}
			vol.Size = req.Size
		}
	} else {
		// Resolve server location before creating volume
		var srvResp struct {
			Server struct {
				Datacenter struct {
					Location struct{ Name string `json:"name"` } `json:"location"`
				} `json:"datacenter"`
			} `json:"server"`
		}
		if err := c.api.Do(ctx, "GET", fmt.Sprintf("/servers/%s", srv.ID), nil, &srvResp); err != nil {
			return nil, fmt.Errorf("get server location: %w", err)
		}

		body := map[string]any{
			"name":      req.Name,
			"size":      req.Size,
			"location":  srvResp.Server.Datacenter.Location.Name,
			"labels":    req.Labels,
			"automount": false,
			"format":    "xfs",
		}

		var createResp struct{ Volume volumeJSON `json:"volume"` }
		if err := c.api.Do(ctx, "POST", "/volumes", body, &createResp); err != nil {
			return nil, fmt.Errorf("create volume: %w", err)
		}
		vol = volumeFrom(createResp.Volume)
	}

	// Attach if not attached to the right server
	if vol.ServerID != srv.ID {
		// Detach from current server if attached elsewhere
		if vol.ServerID != "" {
			if err := c.detachVolume(ctx, vol.ID); err != nil {
				return nil, fmt.Errorf("detach before reattach: %w", err)
			}
		}

		intServerID, _ := strconv.ParseInt(srv.ID, 10, 64)
		var resp struct {
			Action struct {
				ID int64 `json:"id"`
			} `json:"action"`
		}
		if err := c.api.Do(ctx, "POST", fmt.Sprintf("/volumes/%s/actions/attach", vol.ID), map[string]any{
			"server":    intServerID,
			"automount": false,
		}, &resp); err != nil {
			return nil, fmt.Errorf("attach volume: %w", err)
		}
		if resp.Action.ID != 0 {
			if err := c.waitForAction(ctx, resp.Action.ID); err != nil {
				return nil, fmt.Errorf("attach volume action: %w", err)
			}
		}

		// Refresh volume to get updated state
		vol, err = c.getVolume(ctx, vol.ID)
		if err != nil {
			return nil, err
		}
	}

	vol.ServerName = req.ServerName
	return vol, nil
}

func (c *Client) ResizeVolume(ctx context.Context, id string, sizeGB int) error {
	var resp struct {
		Action struct{ ID int64 `json:"id"` } `json:"action"`
	}
	if err := c.api.Do(ctx, "POST", fmt.Sprintf("/volumes/%s/actions/resize", id), map[string]any{"size": sizeGB}, &resp); err != nil {
		return fmt.Errorf("resize volume: %w", err)
	}
	if resp.Action.ID != 0 {
		if err := c.waitForAction(ctx, resp.Action.ID); err != nil {
			return fmt.Errorf("resize action: %w", err)
		}
	}
	return nil
}

func (c *Client) DetachVolume(ctx context.Context, name string) error {
	vol, err := c.getVolumeByName(ctx, name)
	if err != nil {
		return err
	}
	if vol == nil {
		return nil // already gone
	}
	if vol.ServerID == "" {
		return nil // not attached
	}
	return c.detachVolume(ctx, vol.ID)
}

// DeleteVolume detaches (if attached) then deletes the cloud volume.
// 404 = already gone = success.
func (c *Client) DeleteVolume(ctx context.Context, name string) error {
	vol, err := c.getVolumeByName(ctx, name)
	if err != nil {
		return err
	}
	if vol == nil {
		return utils.ErrNotFound
	}
	if vol.ServerID != "" {
		if err := c.detachVolume(ctx, vol.ID); err != nil {
			return fmt.Errorf("detach before delete: %w", err)
		}
	}
	if err := c.api.Do(ctx, "DELETE", fmt.Sprintf("/volumes/%s", vol.ID), nil, nil); err != nil {
		if !utils.IsNotFound(err) {
			return fmt.Errorf("delete volume: %w", err)
		}
	}
	return nil
}

func (c *Client) ListVolumes(ctx context.Context, labels map[string]string) ([]*provider.Volume, error) {
	var parts []string
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	selector := ""
	if len(parts) > 0 {
		selector = "&label_selector=" + strings.Join(parts, ",")
	}

	var resp struct{ Volumes []volumeJSON `json:"volumes"` }
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/volumes?per_page=50%s", selector), nil, &resp); err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}

	volumes := make([]*provider.Volume, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		volumes = append(volumes, volumeFrom(v))
	}
	return volumes, nil
}

func (c *Client) GetPrivateIP(ctx context.Context, serverID string) (string, error) {
	var resp struct {
		Server serverJSON `json:"server"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/servers/%s", serverID), nil, &resp); err != nil {
		return "", fmt.Errorf("get server %s: %w", serverID, err)
	}
	srv := serverFrom(resp.Server)
	return srv.PrivateIP, nil
}

// ResolveDevicePath returns the Linux device path for a Hetzner volume.
// Hetzner API provides this directly as LinuxDevice.
func (c *Client) ResolveDevicePath(vol *provider.Volume) string {
	return vol.DevicePath
}

func (c *Client) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	servers, err := c.ListServers(ctx, nil)
	if err != nil {
		return nil, err
	}
	srvGroup := provider.ResourceGroup{Name: "Servers", Columns: []string{"ID", "Name", "Status", "IPv4", "Private IP"}}
	for _, s := range servers {
		srvGroup.Rows = append(srvGroup.Rows, []string{s.ID, s.Name, string(s.Status), s.IPv4, s.PrivateIP})
	}

	firewalls, err := c.ListAllFirewalls(ctx)
	if err != nil {
		return nil, err
	}
	fwGroup := provider.ResourceGroup{Name: "Firewalls", Columns: []string{"ID", "Name"}}
	for _, fw := range firewalls {
		fwGroup.Rows = append(fwGroup.Rows, []string{fw.ID, fw.Name})
	}

	networks, err := c.ListAllNetworks(ctx)
	if err != nil {
		return nil, err
	}
	netGroup := provider.ResourceGroup{Name: "Networks", Columns: []string{"ID", "Name"}}
	for _, n := range networks {
		netGroup.Rows = append(netGroup.Rows, []string{n.ID, n.Name})
	}

	volumes, err := c.ListVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}
	volGroup := provider.ResourceGroup{Name: "Volumes", Columns: []string{"ID", "Name", "Size", "Server", "Device"}}
	for _, v := range volumes {
		volGroup.Rows = append(volGroup.Rows, []string{v.ID, v.Name, fmt.Sprintf("%dGB", v.Size), v.ServerID, v.DevicePath})
	}

	return []provider.ResourceGroup{srvGroup, fwGroup, netGroup, volGroup}, nil
}

// --- internal helpers ---

func (c *Client) getVolumeByName(ctx context.Context, name string) (*provider.Volume, error) {
	var resp struct{ Volumes []volumeJSON `json:"volumes"` }
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/volumes?name=%s", name), nil, &resp); err != nil {
		return nil, fmt.Errorf("get volume by name: %w", err)
	}
	for _, v := range resp.Volumes {
		if v.Name == name {
			return volumeFrom(v), nil
		}
	}
	return nil, nil
}

func (c *Client) getVolume(ctx context.Context, id string) (*provider.Volume, error) {
	var resp struct{ Volume volumeJSON `json:"volume"` }
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/volumes/%s", id), nil, &resp); err != nil {
		return nil, fmt.Errorf("get volume: %w", err)
	}
	return volumeFrom(resp.Volume), nil
}

func (c *Client) detachVolume(ctx context.Context, volumeID string) error {
	return utils.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		var resp struct {
			Action struct {
				ID int64 `json:"id"`
			} `json:"action"`
		}
		if err := c.api.Do(ctx, "POST", fmt.Sprintf("/volumes/%s/actions/detach", volumeID), nil, &resp); err != nil {
			if strings.Contains(err.Error(), "not attached") {
				return true, nil
			}
			if isLocked(err) {
				return false, nil // retry
			}
			return false, fmt.Errorf("detach volume: %w", err)
		}
		if resp.Action.ID != 0 {
			if err := c.waitForAction(ctx, resp.Action.ID); err != nil {
				return false, fmt.Errorf("detach action: %w", err)
			}
		}
		return true, nil
	})
}

func isLocked(err error) bool {
	if apiErr, ok := err.(*utils.APIError); ok {
		return apiErr.Status == 423
	}
	return false
}

func (c *Client) waitForAction(ctx context.Context, actionID int64) error {
	return utils.Poll(ctx, 2*time.Second, 2*time.Minute, func() (bool, error) {
		var resp struct {
			Action struct {
				ID     int64 `json:"id"`
				Status string `json:"status"`
				Error  *struct{ Message string `json:"message"` } `json:"error"`
			} `json:"action"`
		}
		if err := c.api.Do(ctx, "GET", fmt.Sprintf("/actions/%d", actionID), nil, &resp); err != nil {
			return false, fmt.Errorf("poll action %d: %w", actionID, err)
		}
		switch resp.Action.Status {
		case "success":
			return true, nil
		case "error":
			msg := "unknown error"
			if resp.Action.Error != nil {
				msg = resp.Action.Error.Message
			}
			return false, fmt.Errorf("action %d failed: %s", actionID, msg)
		default:
			return false, nil
		}
	})
}
