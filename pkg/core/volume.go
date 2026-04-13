package core

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/provider"
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

	servers, err := prov.ListServers(ctx, names.Labels())
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

	ssh, err := req.Connect(ctx, serverIP+":22")
	if err != nil {
		return nil, fmt.Errorf("ssh for volume mount: %w", err)
	}
	defer ssh.Close()

	devicePath := prov.ResolveDevicePath(vol)
	if err := infra.MountVolume(ctx, ssh, devicePath, mountPath, out.Writer()); err != nil {
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

	// Check existence
	volumes, _ := prov.ListVolumes(ctx, names.Labels())
	found := false
	for _, v := range volumes {
		if v.Name == volumeName {
			found = true
			break
		}
	}

	if !found {
		out.Success(volumeName + " already deleted")
		return nil
	}

	appServers, err := prov.ListServers(ctx, names.Labels())
	if err == nil {
		for _, s := range appServers {
			ssh, err := req.Connect(ctx, s.IPv4+":22")
			if err != nil {
				out.Warning(fmt.Sprintf("ssh %s for unmount: %s", s.Name, err))
				continue
			}
			if err := infra.UnmountVolume(ctx, ssh, mountPath, out.Writer()); err != nil {
				out.Warning(fmt.Sprintf("unmount on %s: %s", s.Name, err))
			}
			ssh.Close()
		}
	}

	if err := prov.DeleteVolume(ctx, volumeName); err != nil {
		return err
	}
	out.Success(volumeName + " deleted")
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
