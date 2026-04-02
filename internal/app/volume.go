package app

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/provider"
)

type VolumeSetRequest struct {
	Cluster
	Name   string
	Size   int
	Server string
}

type VolumeSetResult struct {
	Volume *provider.Volume
}

func VolumeSet(ctx context.Context, req VolumeSetRequest) (*VolumeSetResult, error) {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return nil, err
	}
	prov, err := req.Compute()
	if err != nil {
		return nil, err
	}

	volumeName := names.Volume(req.Name)
	serverName := names.Server(req.Server)
	mountPath := names.VolumeMountPath(req.Name)

	out.Command("volume", "set", volumeName, "size", fmt.Sprintf("%dGB", req.Size), "server", serverName)

	vol, err := prov.EnsureVolume(ctx, provider.CreateVolumeRequest{
		Name:       volumeName,
		ServerName: serverName,
		Size:       req.Size,
		Labels:     names.Labels(),
	})
	if err != nil {
		return nil, err
	}

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

	if err := infra.MountVolume(ctx, vol, serverIP, mountPath, req.SSHKey); err != nil {
		return nil, fmt.Errorf("mount: %w", err)
	}

	out.Success(fmt.Sprintf("%s → %s on %s", volumeName, mountPath, serverName))
	return &VolumeSetResult{Volume: vol}, nil
}

type VolumeDeleteRequest struct {
	Cluster
	Name string
}

func VolumeDelete(ctx context.Context, req VolumeDeleteRequest) error {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return err
	}
	prov, err := req.Compute()
	if err != nil {
		return err
	}

	volumeName := names.Volume(req.Name)
	mountPath := names.VolumeMountPath(req.Name)

	out.Command("volume", "delete", volumeName)

	servers, err := prov.ListServers(ctx, names.Labels())
	if err == nil {
		for _, s := range servers {
			if err := infra.UnmountVolume(ctx, s.IPv4, mountPath, req.SSHKey); err != nil {
				out.Warning(fmt.Sprintf("unmount on %s: %s", s.Name, err))
			}
		}
	}

	if err := prov.DetachVolume(ctx, volumeName, names.Labels()); err != nil {
		return err
	}
	out.Success("detached")
	return nil
}

type VolumeListRequest struct {
	Cluster
}

func VolumeList(ctx context.Context, req VolumeListRequest) ([]*provider.Volume, error) {
	names, err := req.Names()
	if err != nil {
		return nil, err
	}
	prov, err := req.Compute()
	if err != nil {
		return nil, err
	}
	return prov.ListVolumes(ctx, names.Labels())
}
