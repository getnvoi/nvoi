package hetzner

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/provider"
)

func (c *Client) EnsureVolume(ctx context.Context, req provider.CreateVolumeRequest) (*provider.Volume, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *Client) DetachVolume(ctx context.Context, name string, labels map[string]string) error {
	return fmt.Errorf("not implemented")
}

func (c *Client) ListVolumes(ctx context.Context, labels map[string]string) ([]*provider.Volume, error) {
	return nil, fmt.Errorf("not implemented")
}
