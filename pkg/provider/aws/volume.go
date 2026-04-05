package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func volumeFromEC2(vol ec2types.Volume) *provider.Volume {
	v := &provider.Volume{
		ID:       deref(vol.VolumeId),
		Size:     int(deref32(vol.Size)),
		Location: deref(vol.AvailabilityZone),
	}
	for _, tag := range vol.Tags {
		if deref(tag.Key) == "Name" {
			v.Name = deref(tag.Value)
		}
	}
	if len(vol.Attachments) > 0 {
		v.ServerID = deref(vol.Attachments[0].InstanceId)
		v.DevicePath = deref(vol.Attachments[0].Device)
	}
	return v
}

func (c *Client) EnsureVolume(ctx context.Context, req provider.CreateVolumeRequest) (*provider.Volume, error) {
	if req.Size <= 0 {
		return nil, fmt.Errorf("volume size must be > 0 (got %d)", req.Size)
	}

	// Resolve server
	inst, err := c.findInstanceByName(ctx, req.ServerName)
	if err != nil {
		return nil, fmt.Errorf("resolve server %s: %w", req.ServerName, err)
	}
	if inst == nil {
		return nil, fmt.Errorf("server %s not found — run 'instance set' first", req.ServerName)
	}
	instanceID := deref(inst.InstanceId)
	az := deref(inst.Placement.AvailabilityZone)

	// Find existing volume by name
	vol, err := c.findVolumeByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}

	if vol != nil {
		currentSize := int(deref32(vol.Size))
		if req.Size < currentSize {
			return nil, fmt.Errorf("volume %s is %dGB, requested %dGB — shrinking not supported", req.Name, currentSize, req.Size)
		}
		if req.Size > currentSize {
			if err := c.ResizeVolume(ctx, deref(vol.VolumeId), req.Size); err != nil {
				return nil, err
			}
			vol.Size = aws.Int32(int32(req.Size))
		}
	}

	if vol == nil {
		// Create in same AZ as server
		createResp, err := c.ec2.CreateVolume(ctx, &ec2.CreateVolumeInput{
			AvailabilityZone:  aws.String(az),
			Size:              aws.Int32(int32(req.Size)),
			VolumeType:        ec2types.VolumeTypeGp3,
			TagSpecifications: tagSpec(ec2types.ResourceTypeVolume, req.Name, req.Labels),
		})
		if err != nil {
			return nil, fmt.Errorf("create volume: %w", err)
		}
		vol = &ec2types.Volume{
			VolumeId:         createResp.VolumeId,
			Size:             createResp.Size,
			AvailabilityZone: createResp.AvailabilityZone,
			State:            createResp.State,
			Tags:             []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String(req.Name)}},
		}
	}

	volID := deref(vol.VolumeId)

	// Attach if not attached to the right server
	currentServer := ""
	if len(vol.Attachments) > 0 {
		currentServer = deref(vol.Attachments[0].InstanceId)
	}

	if currentServer != instanceID {
		if currentServer != "" {
			c.ec2.DetachVolume(ctx, &ec2.DetachVolumeInput{VolumeId: aws.String(volID)})
			c.waitForVolumeAvailable(ctx, volID)
		}

		// Find next available device
		device := c.nextDevice(ctx, instanceID)

		c.waitForVolumeAvailable(ctx, volID)
		_, err = c.ec2.AttachVolume(ctx, &ec2.AttachVolumeInput{
			VolumeId:   aws.String(volID),
			InstanceId: aws.String(instanceID),
			Device:     aws.String(device),
		})
		if err != nil {
			return nil, fmt.Errorf("attach volume: %w", err)
		}

		// Wait for attachment to complete
		if err := c.waitForVolumeAttached(ctx, volID); err != nil {
			return nil, fmt.Errorf("volume %s did not attach: %w", volID, err)
		}
	}

	// Re-fetch to get attachment info (device path)
	refreshed, err := c.findVolumeByName(ctx, req.Name)
	if err != nil {
		return nil, fmt.Errorf("refresh volume: %w", err)
	}
	if refreshed != nil {
		vol = refreshed
	}

	result := volumeFromEC2(*vol)
	result.ServerName = req.ServerName
	return result, nil
}

func (c *Client) ResizeVolume(ctx context.Context, id string, sizeGB int) error {
	_, err := c.ec2.ModifyVolume(ctx, &ec2.ModifyVolumeInput{
		VolumeId: aws.String(id),
		Size:     aws.Int32(int32(sizeGB)),
	})
	if err != nil {
		return fmt.Errorf("resize volume %s: %w", id, err)
	}
	// Wait for modification to complete
	return utils.Poll(ctx, 5*time.Second, 5*time.Minute, func() (bool, error) {
		resp, err := c.ec2.DescribeVolumesModifications(ctx, &ec2.DescribeVolumesModificationsInput{
			VolumeIds: []string{id},
		})
		if err != nil {
			return false, nil
		}
		if len(resp.VolumesModifications) > 0 {
			state := resp.VolumesModifications[0].ModificationState
			return state == ec2types.VolumeModificationStateCompleted || state == ec2types.VolumeModificationStateOptimizing, nil
		}
		return false, nil
	})
}

func (c *Client) DetachVolume(ctx context.Context, name string) error {
	vol, err := c.findVolumeByName(ctx, name)
	if err != nil || vol == nil {
		return nil
	}
	if len(vol.Attachments) == 0 {
		return nil
	}
	_, err = c.ec2.DetachVolume(ctx, &ec2.DetachVolumeInput{
		VolumeId: vol.VolumeId,
	})
	if err != nil {
		return fmt.Errorf("detach volume: %w", err)
	}
	return c.waitForVolumeAvailable(ctx, deref(vol.VolumeId))
}

func (c *Client) DeleteVolume(ctx context.Context, name string) error {
	vol, err := c.findVolumeByName(ctx, name)
	if err != nil {
		return err
	}
	if vol == nil {
		return utils.ErrNotFound
	}
	if len(vol.Attachments) > 0 {
		c.ec2.DetachVolume(ctx, &ec2.DetachVolumeInput{VolumeId: vol.VolumeId})
		c.waitForVolumeAvailable(ctx, deref(vol.VolumeId))
	}
	_, err = c.ec2.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: vol.VolumeId})
	return err
}

func (c *Client) ListVolumes(ctx context.Context, labels map[string]string) ([]*provider.Volume, error) {
	var filters []ec2types.Filter
	for k, v := range labels {
		filters = append(filters, ec2types.Filter{
			Name: aws.String("tag:" + k), Values: []string{v},
		})
	}
	resp, err := c.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	var out []*provider.Volume
	for _, vol := range resp.Volumes {
		out = append(out, volumeFromEC2(vol))
	}
	return out, nil
}

func (c *Client) GetPrivateIP(ctx context.Context, serverID string) (string, error) {
	resp, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{serverID},
	})
	if err != nil {
		return "", fmt.Errorf("get instance %s: %w", serverID, err)
	}
	if len(resp.Reservations) > 0 && len(resp.Reservations[0].Instances) > 0 {
		return deref(resp.Reservations[0].Instances[0].PrivateIpAddress), nil
	}
	return "", nil
}

// ResolveDevicePath returns the OS block device path for an AWS EBS volume.
// NVMe instances expose EBS as /dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_vol<id>.
// The API-returned DevicePath (/dev/xvdf) is just a hint — not the real device.
func (c *Client) ResolveDevicePath(vol *provider.Volume) string {
	volID := strings.ReplaceAll(vol.ID, "-", "")
	return "/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_" + volID
}

// ── Internal helpers ────────────────────────────────────────────────────────────

func (c *Client) findVolumeByName(ctx context.Context, name string) (*ec2types.Volume, error) {
	resp, err := c.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:Name"), Values: []string{name}},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Volumes) > 0 {
		return &resp.Volumes[0], nil
	}
	return nil, nil
}

func (c *Client) waitForVolumeAttached(ctx context.Context, volumeID string) error {
	return utils.Poll(ctx, 2*time.Second, 2*time.Minute, func() (bool, error) {
		resp, err := c.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			VolumeIds: []string{volumeID},
		})
		if err != nil {
			return false, nil
		}
		if len(resp.Volumes) > 0 && len(resp.Volumes[0].Attachments) > 0 {
			return resp.Volumes[0].Attachments[0].State == ec2types.VolumeAttachmentStateAttached, nil
		}
		return false, nil
	})
}

func (c *Client) waitForVolumeAvailable(ctx context.Context, volumeID string) error {
	return utils.Poll(ctx, 2*time.Second, time.Minute, func() (bool, error) {
		resp, err := c.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			VolumeIds: []string{volumeID},
		})
		if err != nil {
			return false, nil
		}
		if len(resp.Volumes) > 0 {
			return resp.Volumes[0].State == ec2types.VolumeStateAvailable, nil
		}
		return false, nil
	})
}

// nextDevice finds the next available device name (/dev/xvdf, /dev/xvdg, etc.)
func (c *Client) nextDevice(ctx context.Context, instanceID string) string {
	resp, _ := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	used := map[string]bool{}
	if resp != nil {
		for _, res := range resp.Reservations {
			for _, inst := range res.Instances {
				for _, bdm := range inst.BlockDeviceMappings {
					used[deref(bdm.DeviceName)] = true
				}
			}
		}
	}
	for _, letter := range "fghijklmnop" {
		dev := fmt.Sprintf("/dev/xvd%c", letter)
		if !used[dev] {
			return dev
		}
	}
	return "/dev/xvdf"
}

func deref32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}
