package scaleway

import (
	"context"
	"fmt"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// Block Storage API — separate from Instance API.
// Volumes attached by PATCHing server's indexed volume map.
// Device paths NOT returned by API — detected via SSH (infra/ handles this).

type blockVolumeJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"` // bytes
	Status     string `json:"status"`
	Zone       string `json:"zone"`
	References []struct {
		ProductResourceType string `json:"product_resource_type"`
		ProductResourceID   string `json:"product_resource_id"`
	} `json:"references"`
}

func blockVolumeFrom(v blockVolumeJSON) *provider.Volume {
	vol := &provider.Volume{
		ID:       v.ID,
		Name:     v.Name,
		Size:     int(v.Size / 1_000_000_000), // bytes → GB
		Location: v.Zone,
	}
	for _, ref := range v.References {
		if ref.ProductResourceType == "instance_server" {
			vol.ServerID = ref.ProductResourceID
			break
		}
	}
	return vol
}

// EnsureVolume creates or finds a volume by name, attaches to the named server.
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
		return nil, fmt.Errorf("server %s not found — run 'instance set' first", req.ServerName)
	}

	// Find existing volume
	vol, err := c.getVolumeByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}

	if vol != nil {
		if req.Size < vol.Size {
			return nil, fmt.Errorf("volume %s is %dGB, requested %dGB — shrinking not supported", vol.Name, vol.Size, req.Size)
		}
		if req.Size > vol.Size {
			if err := c.ResizeVolume(ctx, vol.ID, req.Size); err != nil {
				return nil, err
			}
			vol.Size = req.Size
		}
	}

	if vol == nil {
		// Create
		body := struct {
			Name      string `json:"name"`
			PerfIops  int    `json:"perf_iops"`
			FromEmpty struct {
				Size int64 `json:"size"`
			} `json:"from_empty"`
			ProjectID string `json:"project_id"`
		}{
			Name:      req.Name,
			PerfIops:  5000,
			ProjectID: c.projectID,
		}
		body.FromEmpty.Size = int64(req.Size) * 1_000_000_000

		var resp blockVolumeJSON
		if err := c.api.Do(ctx, "POST", c.blockPath("/volumes"), body, &resp); err != nil {
			return nil, fmt.Errorf("create volume: %w", err)
		}
		vol = blockVolumeFrom(resp)
	}

	// Attach if not attached to the right server
	if vol.ServerID != srv.ID {
		if vol.ServerID != "" {
			if err := c.detachVolumeByID(ctx, vol.ID); err != nil {
				return nil, fmt.Errorf("detach before reattach: %w", err)
			}
		}
		if err := c.attachVolume(ctx, vol.ID, srv.ID); err != nil {
			return nil, fmt.Errorf("attach volume: %w", err)
		}
		// Refresh
		vol, err = c.getVolume(ctx, vol.ID)
		if err != nil {
			return nil, err
		}
	}

	vol.ServerName = req.ServerName
	return vol, nil
}

func (c *Client) ResizeVolume(ctx context.Context, id string, sizeGB int) error {
	body := struct {
		Size int64 `json:"size"`
	}{
		Size: int64(sizeGB) * 1_000_000_000,
	}
	if err := c.api.Do(ctx, "PATCH", c.blockPath(fmt.Sprintf("/volumes/%s", id)), body, nil); err != nil {
		return fmt.Errorf("resize volume %s: %w", id, err)
	}
	return nil
}

func (c *Client) DetachVolume(ctx context.Context, name string) error {
	vol, err := c.getVolumeByName(ctx, name)
	if err != nil || vol == nil {
		return nil
	}
	if vol.ServerID == "" {
		return nil
	}
	return c.detachVolumeByID(ctx, vol.ID)
}

func (c *Client) DeleteVolume(ctx context.Context, name string) error {
	vol, err := c.getVolumeByName(ctx, name)
	if err != nil {
		return err
	}
	if vol == nil {
		return utils.ErrNotFound
	}
	if vol.ServerID != "" {
		if err := c.detachVolumeByID(ctx, vol.ID); err != nil {
			return fmt.Errorf("detach before delete: %w", err)
		}
	}
	if err := c.api.Do(ctx, "DELETE", c.blockPath(fmt.Sprintf("/volumes/%s", vol.ID)), nil, nil); err != nil {
		if !utils.IsNotFound(err) {
			return fmt.Errorf("delete volume: %w", err)
		}
	}
	return nil
}

func (c *Client) ListVolumes(ctx context.Context, labels map[string]string) ([]*provider.Volume, error) {
	var resp struct {
		Volumes []blockVolumeJSON `json:"volumes"`
	}
	if err := c.api.Do(ctx, "GET", c.blockPath("/volumes"), nil, &resp); err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	volumes := make([]*provider.Volume, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		volumes = append(volumes, blockVolumeFrom(v))
	}
	return volumes, nil
}

// ResolveDevicePath returns the OS block device path for a Scaleway SBS volume.
// SBS volumes appear as /dev/disk/by-id/scsi-0SCW_sbs_volume-<id>.
func (c *Client) ResolveDevicePath(vol *provider.Volume) string {
	return "/dev/disk/by-id/scsi-0SCW_sbs_volume-" + vol.ID
}

// ── Internal helpers ────────────────────────────────────────────────────────────

func (c *Client) getVolume(ctx context.Context, id string) (*provider.Volume, error) {
	var resp blockVolumeJSON
	if err := c.api.Do(ctx, "GET", c.blockPath(fmt.Sprintf("/volumes/%s", id)), nil, &resp); err != nil {
		return nil, fmt.Errorf("get volume: %w", err)
	}
	return blockVolumeFrom(resp), nil
}

func (c *Client) getVolumeByName(ctx context.Context, name string) (*provider.Volume, error) {
	volumes, err := c.ListVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, v := range volumes {
		if v.Name == name {
			return v, nil
		}
	}
	return nil, nil
}

func (c *Client) attachVolume(ctx context.Context, volumeID, serverID string) error {
	// Wait for volume to be available
	if err := c.waitForVolumeAvailable(ctx, volumeID); err != nil {
		return err
	}

	// Get server's current volumes
	var serverResp struct {
		Server struct {
			Volumes map[string]any `json:"volumes"`
		} `json:"server"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/servers/%s", serverID), nil, &serverResp); err != nil {
		return fmt.Errorf("get server volumes: %w", err)
	}

	volumes := serverResp.Server.Volumes
	if volumes == nil {
		volumes = make(map[string]any)
	}

	// Find next index
	maxIdx := -1
	for k := range volumes {
		var idx int
		if _, err := fmt.Sscanf(k, "%d", &idx); err == nil && idx > maxIdx {
			maxIdx = idx
		}
	}

	// Add volume at next index
	newVolumes := make(map[string]any)
	for k, v := range volumes {
		newVolumes[k] = v
	}
	newVolumes[fmt.Sprintf("%d", maxIdx+1)] = map[string]any{
		"id":          volumeID,
		"volume_type": "sbs_volume",
	}

	return c.doInstance(ctx, "PATCH", fmt.Sprintf("/servers/%s", serverID), map[string]any{"volumes": newVolumes}, nil)
}

func (c *Client) detachVolumeByID(ctx context.Context, volumeID string) error {
	// Find server holding this volume
	var serversResp struct {
		Servers []serverJSON `json:"servers"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/servers?project=%s", c.projectID), nil, &serversResp); err != nil {
		return err
	}

	for _, server := range serversResp.Servers {
		var serverResp struct {
			Server struct {
				Volumes map[string]any `json:"volumes"`
			} `json:"server"`
		}
		if err := c.doInstance(ctx, "GET", fmt.Sprintf("/servers/%s", server.ID), nil, &serverResp); err != nil {
			continue
		}

		foundIdx := ""
		for idx, volData := range serverResp.Server.Volumes {
			volMap, ok := volData.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := volMap["id"].(string); ok && id == volumeID {
				foundIdx = idx
				break
			}
		}
		if foundIdx == "" {
			continue
		}

		newVolumes := make(map[string]any)
		for k, v := range serverResp.Server.Volumes {
			if k != foundIdx {
				newVolumes[k] = v
			}
		}

		if err := c.doInstance(ctx, "PATCH", fmt.Sprintf("/servers/%s", server.ID), map[string]any{"volumes": newVolumes}, nil); err != nil {
			return fmt.Errorf("detach volume: %w", err)
		}

		return c.waitForVolumeAvailable(ctx, volumeID)
	}

	return nil // not attached
}

func (c *Client) waitForVolumeAvailable(ctx context.Context, volumeID string) error {
	return utils.Poll(ctx, 2*time.Second, time.Minute, func() (bool, error) {
		var resp blockVolumeJSON
		if err := c.api.Do(ctx, "GET", c.blockPath(fmt.Sprintf("/volumes/%s", volumeID)), nil, &resp); err != nil {
			return false, nil
		}
		return resp.Status == "available", nil
	})
}
