package app

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/provider"
)

type VolumeSetRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Name        string
	Size        int
	Server      string // server name (e.g. "master")
}

type VolumeSetResult struct {
	Volume *provider.Volume
}

func VolumeSet(ctx context.Context, req VolumeSetRequest) (*VolumeSetResult, error) {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return nil, err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return nil, err
	}

	volumeName := names.Volume(req.Name)
	serverName := names.Server(req.Server)
	mountPath := names.VolumeMountPath(req.Name)

	fmt.Printf("==> volume set %s (%dGB on %s)\n", volumeName, req.Size, serverName)

	// EnsureVolume — provider resolves server, creates/finds volume, attaches
	vol, err := prov.EnsureVolume(ctx, provider.CreateVolumeRequest{
		Name:       volumeName,
		ServerName: serverName,
		Size:       req.Size,
		Labels:     names.Labels(),
	})
	if err != nil {
		return nil, err
	}

	// Find server IP for SSH mounting
	masterLabels := names.Labels()
	masterLabels["role"] = "master"
	servers, err := prov.ListServers(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	var serverIP string
	for _, s := range servers {
		if s.Name == serverName {
			serverIP = s.IPv4
			break
		}
	}
	if serverIP == "" {
		return nil, fmt.Errorf("server %s not found", serverName)
	}

	// Mount via SSH
	if err := infra.MountVolume(ctx, vol, serverIP, mountPath, req.SSHKey); err != nil {
		return nil, fmt.Errorf("mount: %w", err)
	}

	fmt.Printf("  ✓ %s → %s on %s\n", volumeName, mountPath, serverName)
	return &VolumeSetResult{Volume: vol}, nil
}

type VolumeDeleteRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Name        string
}

func VolumeDelete(ctx context.Context, req VolumeDeleteRequest) error {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return err
	}

	volumeName := names.Volume(req.Name)
	mountPath := names.VolumeMountPath(req.Name)

	fmt.Printf("==> volume delete %s (detach only — data preserved)\n", volumeName)

	// Unmount on the server before detaching at provider level
	// Find which server the volume is attached to
	servers, err := prov.ListServers(ctx, names.Labels())
	if err == nil {
		for _, s := range servers {
			if err := infra.UnmountVolume(ctx, s.IPv4, mountPath, req.SSHKey); err != nil {
				// Non-fatal — server might be gone or mount might not exist
				fmt.Printf("  warning: unmount on %s: %s\n", s.Name, err)
			}
		}
	}

	if err := prov.DetachVolume(ctx, volumeName, names.Labels()); err != nil {
		return err
	}
	fmt.Printf("  ✓ detached\n")
	return nil
}

type VolumeListRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
}

func VolumeList(ctx context.Context, req VolumeListRequest) ([]*provider.Volume, error) {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return nil, err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return nil, err
	}
	return prov.ListVolumes(ctx, names.Labels())
}
