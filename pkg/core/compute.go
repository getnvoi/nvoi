package core

import (
	"context"
	"errors"
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
	DiskGB     int
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
		DiskGB:       req.DiskGB,
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

	// Clear stale known host — the server was just created/ensured,
	// IP may have been recycled from a previous server.
	// "not found" is expected on first deploy. Write failures are real —
	// a stale entry causes ErrHostKeyChanged with misleading guidance.
	if err := infra.ClearKnownHost(srv.IPv4 + ":22"); err != nil {
		if !strings.Contains(err.Error(), "no known host") {
			out.Warning(fmt.Sprintf("clear known host %s: %s", srv.IPv4, err))
		}
	}

	// Wait for SSH and connect — all infra operations use this connection.
	out.Progress(fmt.Sprintf("waiting for SSH on %s", srv.IPv4))
	var ssh utils.SSHClient
	sshCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := utils.Poll(sshCtx, 2*time.Second, 5*time.Minute, func() (bool, error) {
		conn, err := req.Connect(ctx, srv.IPv4+":22")
		if err != nil {
			if errors.Is(err, infra.ErrHostKeyChanged) {
				return false, err // hard error — server was recreated
			}
			if errors.Is(err, infra.ErrAuthFailed) {
				return false, err // hard error — wrong SSH key
			}
			return false, nil // transient (refused, timeout) — retry
		}
		ssh = conn
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("SSH not reachable on %s: %w", srv.IPv4, err)
	}
	out.Success("SSH ready")

	out.Progress("ensuring swap")
	if err := infra.EnsureSwap(ctx, ssh); err != nil {
		out.Warning(fmt.Sprintf("swap: %s", err))
	} else {
		out.Success("swap ready")
	}

	out.Progress("ensuring Docker")
	if err := infra.EnsureDocker(ctx, ssh); err != nil {
		ssh.Close()
		return nil, fmt.Errorf("docker on %s: %w", srv.IPv4, err)
	}
	out.Success("Docker ready")

	// Reconnect SSH so the session picks up the docker group membership.
	ssh.Close()
	ssh, err = req.Connect(ctx, srv.IPv4+":22")
	if err != nil {
		return nil, fmt.Errorf("reconnect SSH after docker setup: %w", err)
	}
	defer ssh.Close()

	srvNode := infra.Node{PublicIP: srv.IPv4, PrivateIP: srv.PrivateIP}
	var masterSSH utils.SSHClient

	if req.Worker {
		master, err := FindMaster(ctx, prov, names)
		if err != nil {
			return nil, err
		}
		out.Progress(fmt.Sprintf("joining cluster via master %s", master.IPv4))

		masterSSH, err = req.Connect(ctx, master.IPv4+":22")
		if err != nil {
			return nil, fmt.Errorf("ssh master for worker join: %w", err)
		}
		defer masterSSH.Close()

		masterNode := infra.Node{PublicIP: master.IPv4, PrivateIP: master.PrivateIP}
		if err := infra.JoinK3sWorker(ctx, masterSSH, ssh, srvNode, masterNode, out.Writer()); err != nil {
			return nil, fmt.Errorf("k3s worker join: %w", err)
		}
		out.Success("joined cluster")

		// Label via master SSH
		if err := kube.LabelNode(ctx, masterSSH, names.Server(req.Name), req.Name); err != nil {
			return nil, fmt.Errorf("label node: %w", err)
		}
	} else {
		out.Progress("installing k3s master")
		if err := infra.InstallK3sMaster(ctx, ssh, srvNode, out.Writer()); err != nil {
			return nil, fmt.Errorf("k3s master: %w", err)
		}
		out.Success("k3s master ready")

		if err := infra.EnsureRegistry(ctx, ssh, srvNode, out.Writer()); err != nil {
			return nil, fmt.Errorf("registry: %w", err)
		}

		// Label via this SSH (it's the master)
		if err := kube.LabelNode(ctx, ssh, names.Server(req.Name), req.Name); err != nil {
			return nil, fmt.Errorf("label node: %w", err)
		}
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

	// Check existence first for informational message.
	servers, _ := prov.ListServers(ctx, names.Labels())
	found := false
	for _, s := range servers {
		if s.Name == serverName {
			found = true
			break
		}
	}

	if !found {
		out.Success(serverName + " already deleted")
		return nil
	}

	// DeleteServer handles: detach firewall → detach volumes → delete → wait gone.
	if err := prov.DeleteServer(ctx, provider.DeleteServerRequest{
		Name:   serverName,
		Labels: names.Labels(),
	}); err != nil {
		return err
	}
	out.Success(serverName + " deleted")
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
