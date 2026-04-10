package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type ComputeSetRequest struct {
	Cluster
	Name       string
	ServerType string
	Region     string
	Worker     bool
}

type ComputeSetResult struct {
	Server *provider.Server
}

func ComputeSet(ctx context.Context, req ComputeSetRequest) (*ComputeSetResult, error) {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return nil, err
	}
	prov, err := req.Compute()
	if err != nil {
		return nil, err
	}

	pubKey, err := utils.DerivePublicKey(req.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}

	serverName := names.Server(req.Name)
	userData, err := infra.RenderCloudInit(strings.TrimSpace(pubKey), serverName)
	if err != nil {
		return nil, err
	}

	labels := names.Labels()
	role := "master"
	if req.Worker {
		role = "worker"
	}
	labels["role"] = role

	out.Command("instance", "set", names.Server(req.Name), "role", role)

	srv, err := prov.EnsureServer(ctx, provider.CreateServerRequest{
		Name:         names.Server(req.Name),
		ServerType:   req.ServerType,
		Image:        utils.DefaultImage,
		Location:     req.Region,
		UserData:     userData,
		FirewallName: names.Firewall(),
		NetworkName:  names.Network(),
		Labels:       labels,
	})
	if err != nil {
		return nil, err
	}

	// Always resolve private IP via provider — ListServers/EnsureServer
	// may not populate it (e.g. Scaleway requires IPAM lookup).
	if srv.PrivateIP == "" {
		ip, err := prov.GetPrivateIP(ctx, srv.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve private IP for %s: %w", srv.Name, err)
		}
		if ip == "" {
			return nil, fmt.Errorf("server %s has no private IP — private network may not be attached", srv.Name)
		}
		srv.PrivateIP = ip
	}

	out.Progress(fmt.Sprintf("waiting for SSH on %s", srv.IPv4))
	sshCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := infra.WaitSSH(sshCtx, srv.IPv4+":22", req.SSHKey); err != nil {
		return nil, fmt.Errorf("SSH not reachable on %s: %w", srv.IPv4, err)
	}
	out.Success("SSH ready")

	out.Progress("ensuring Docker")
	if err := infra.EnsureDocker(ctx, srv.IPv4, req.SSHKey); err != nil {
		return nil, fmt.Errorf("docker on %s: %w", srv.IPv4, err)
	}
	out.Success("Docker ready")

	var masterIP string
	if req.Worker {
		master, err := FindMaster(ctx, prov, names)
		if err != nil {
			return nil, err
		}
		masterIP = master.IPv4
		out.Progress(fmt.Sprintf("joining cluster via master %s", master.IPv4))
		workerNode := infra.Node{PublicIP: srv.IPv4, PrivateIP: srv.PrivateIP}
		masterNode := infra.Node{PublicIP: master.IPv4, PrivateIP: master.PrivateIP}
		if err := infra.JoinK3sWorker(ctx, workerNode, masterNode, req.SSHKey, out.Writer()); err != nil {
			return nil, fmt.Errorf("k3s worker join: %w", err)
		}
		out.Success("joined cluster")
	} else {
		masterIP = srv.IPv4
		srvNode := infra.Node{PublicIP: srv.IPv4, PrivateIP: srv.PrivateIP}
		out.Progress("installing k3s master")
		if err := infra.InstallK3sMaster(ctx, srvNode, req.SSHKey, out.Writer()); err != nil {
			return nil, fmt.Errorf("k3s master: %w", err)
		}
		out.Success("k3s master ready")

		if err := infra.EnsureRegistry(ctx, srvNode, req.SSHKey, out.Writer()); err != nil {
			return nil, fmt.Errorf("registry: %w", err)
		}
	}

	if err := kube.LabelNode(ctx, infra.Node{PublicIP: masterIP}, req.SSHKey, names.Server(req.Name), req.Name); err != nil {
		return nil, fmt.Errorf("label node: %w", err)
	}
	out.Success(fmt.Sprintf("node labeled %s=%s", utils.LabelNvoiRole, req.Name))

	out.Success(fmt.Sprintf("%s %s (private: %s)", names.Server(req.Name), srv.IPv4, srv.PrivateIP))
	return &ComputeSetResult{Server: srv}, nil
}

type ComputeDeleteRequest struct {
	Cluster
	Name string
}

func ComputeDelete(ctx context.Context, req ComputeDeleteRequest) error {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return err
	}
	prov, err := req.Compute()
	if err != nil {
		return err
	}

	serverName := names.Server(req.Name)
	out.Command("instance", "delete", serverName)

	// Detach any volumes attached to this server before deleting.
	volumes, _ := prov.ListVolumes(ctx, names.Labels())
	for _, vol := range volumes {
		if vol.ServerName == serverName {
			_ = prov.DetachVolume(ctx, vol.Name)
		}
	}

	// Node drain + kubectl delete node is handled by the reconciler (DrainNode)
	// before this function is called. ComputeDelete only deletes the provider server.

	return prov.DeleteServer(ctx, provider.DeleteServerRequest{
		Name:   serverName,
		Labels: names.Labels(),
	})
}

type ComputeListRequest struct {
	Cluster
}

func ComputeList(ctx context.Context, req ComputeListRequest) ([]*provider.Server, error) {
	names, err := req.Names()
	if err != nil {
		return nil, err
	}
	prov, err := req.Compute()
	if err != nil {
		return nil, err
	}
	return prov.ListServers(ctx, names.Labels())
}

// ErrNoMaster is returned when the master server doesn't exist.
// Delete functions treat this as idempotent success — no cluster = nothing to delete.
var ErrNoMaster = fmt.Errorf("no master server found")

func FindMaster(ctx context.Context, prov provider.ComputeProvider, names *utils.Names) (*provider.Server, error) {
	masterLabels := names.Labels()
	masterLabels["role"] = "master"
	masters, err := prov.ListServers(ctx, masterLabels)
	if err != nil {
		return nil, fmt.Errorf("find master: %w", err)
	}
	if len(masters) == 0 {
		return nil, ErrNoMaster
	}
	master := masters[0]
	if master.PrivateIP == "" {
		ip, err := prov.GetPrivateIP(ctx, master.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve private IP for master: %w", err)
		}
		if ip == "" {
			return nil, fmt.Errorf("master %s has no private IP — private network may not be attached", master.Name)
		}
		master.PrivateIP = ip
	}
	return master, nil
}
