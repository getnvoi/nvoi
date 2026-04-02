package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/kube"
	"github.com/getnvoi/nvoi/internal/provider"
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

	pubKey, err := core.DerivePublicKey(req.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}

	userData, err := infra.RenderCloudInit(strings.TrimSpace(pubKey))
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
		Image:        core.DefaultImage,
		Location:     req.Region,
		UserData:     userData,
		FirewallName: names.Firewall(),
		NetworkName:  names.Network(),
		Labels:       labels,
	})
	if err != nil {
		return nil, err
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
		if err := infra.JoinK3sWorker(ctx, srv.IPv4, srv.PrivateIP, master.IPv4, master.PrivateIP, req.SSHKey, out.Writer()); err != nil {
			return nil, fmt.Errorf("k3s worker join: %w", err)
		}
		out.Success("joined cluster")
	} else {
		masterIP = srv.IPv4
		out.Progress("installing k3s master")
		if err := infra.InstallK3sMaster(ctx, srv.IPv4, srv.PrivateIP, req.SSHKey, out.Writer()); err != nil {
			return nil, fmt.Errorf("k3s master: %w", err)
		}
		out.Success("k3s master ready")

		if err := infra.EnsureRegistry(ctx, srv.IPv4, srv.PrivateIP, req.SSHKey, out.Writer()); err != nil {
			return nil, fmt.Errorf("registry: %w", err)
		}
	}

	if err := kube.LabelNode(ctx, masterIP, req.SSHKey, names.Server(req.Name), req.Name); err != nil {
		return nil, fmt.Errorf("label node: %w", err)
	}
	out.Success(fmt.Sprintf("node labeled %s=%s", core.LabelNvoiRole, req.Name))

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
	if err := prov.DeleteServer(ctx, provider.DeleteServerRequest{
		Name:         serverName,
		FirewallName: names.Firewall(),
		NetworkName:  names.Network(),
		Labels:       names.Labels(),
	}); err != nil {
		return err
	}
	out.Success("deleted")
	return nil
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

func FindMaster(ctx context.Context, prov provider.ComputeProvider, names *core.Names) (*provider.Server, error) {
	masterLabels := names.Labels()
	masterLabels["role"] = "master"
	masters, err := prov.ListServers(ctx, masterLabels)
	if err != nil {
		return nil, fmt.Errorf("find master: %w", err)
	}
	if len(masters) == 0 {
		return nil, fmt.Errorf("no master server found — run 'instance set <name>' first (without --worker)")
	}
	return masters[0], nil
}
