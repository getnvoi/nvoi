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
	if req.Worker {
		labels["role"] = "worker"
	} else {
		labels["role"] = "master"
	}

	fmt.Printf("==> compute set %s (role: %s)\n", names.Server(req.Name), labels["role"])

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

	fmt.Printf("  waiting for SSH on %s...\n", srv.IPv4)
	sshCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := infra.WaitSSH(sshCtx, srv.IPv4+":22", req.SSHKey); err != nil {
		return nil, fmt.Errorf("SSH not reachable on %s: %w", srv.IPv4, err)
	}
	fmt.Printf("  ✓ SSH ready\n")

	fmt.Printf("  ensuring Docker...\n")
	if err := infra.EnsureDocker(ctx, srv.IPv4, req.SSHKey); err != nil {
		return nil, fmt.Errorf("docker on %s: %w", srv.IPv4, err)
	}
	fmt.Printf("  ✓ Docker ready\n")

	var masterIP string
	if req.Worker {
		master, err := FindMaster(ctx, prov, names)
		if err != nil {
			return nil, err
		}
		masterIP = master.IPv4
		fmt.Printf("  joining cluster via master %s...\n", master.IPv4)
		if err := infra.JoinK3sWorker(ctx, srv.IPv4, srv.PrivateIP, master.IPv4, master.PrivateIP, req.SSHKey); err != nil {
			return nil, fmt.Errorf("k3s worker join: %w", err)
		}
		fmt.Printf("  ✓ joined cluster\n")
	} else {
		masterIP = srv.IPv4
		fmt.Printf("  installing k3s master...\n")
		if err := infra.InstallK3sMaster(ctx, srv.IPv4, srv.PrivateIP, req.SSHKey); err != nil {
			return nil, fmt.Errorf("k3s master: %w", err)
		}
		fmt.Printf("  ✓ k3s master ready\n")

		if err := infra.EnsureRegistry(ctx, srv.IPv4, srv.PrivateIP, req.SSHKey); err != nil {
			return nil, fmt.Errorf("registry: %w", err)
		}
	}

	// Label the k8s node — idempotent, runs every deploy.
	if err := kube.LabelNode(ctx, masterIP, req.SSHKey, names.Server(req.Name), req.Name); err != nil {
		return nil, fmt.Errorf("label node: %w", err)
	}
	fmt.Printf("  ✓ node labeled %s=%s\n", core.LabelNvoiRole, req.Name)

	fmt.Printf("  ✓ %s %s (private: %s)\n", names.Server(req.Name), srv.IPv4, srv.PrivateIP)
	return &ComputeSetResult{Server: srv}, nil
}

type ComputeDeleteRequest struct {
	Cluster
	Name string
}

func ComputeDelete(ctx context.Context, req ComputeDeleteRequest) error {
	names, err := req.Names()
	if err != nil {
		return err
	}
	prov, err := req.Compute()
	if err != nil {
		return err
	}

	serverName := names.Server(req.Name)
	fmt.Printf("==> compute delete %s\n", serverName)
	if err := prov.DeleteServer(ctx, provider.DeleteServerRequest{
		Name:         serverName,
		FirewallName: names.Firewall(),
		NetworkName:  names.Network(),
		Labels:       names.Labels(),
	}); err != nil {
		return err
	}
	fmt.Printf("  ✓ deleted\n")
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

// FindMaster finds the server labeled role=master for this app+env.
func FindMaster(ctx context.Context, prov provider.ComputeProvider, names *core.Names) (*provider.Server, error) {
	masterLabels := names.Labels()
	masterLabels["role"] = "master"
	masters, err := prov.ListServers(ctx, masterLabels)
	if err != nil {
		return nil, fmt.Errorf("find master: %w", err)
	}
	if len(masters) == 0 {
		return nil, fmt.Errorf("no master server found — run 'compute set <name> --provider ...' first (without --worker)")
	}
	return masters[0], nil
}
