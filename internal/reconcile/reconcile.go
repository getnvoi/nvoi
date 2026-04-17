package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
)

// Deploy reconciles live infrastructure to match the YAML config.
func Deploy(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if err := cfg.Resolve(); err != nil {
		return err
	}

	live, err := DescribeLive(ctx, dc, cfg)
	if err != nil {
		return err
	}

	// Create desired servers. Orphans are NOT removed yet — workloads
	// must move to new nodes first (zero-downtime server replacement).
	if err := ServersAdd(ctx, dc, live, cfg); err != nil {
		return err
	}

	// Master is now guaranteed to exist. Establish a single SSH connection
	// for all remaining operations, plus a kube client over the same tunnel.
	master, _, _, err := dc.Cluster.Master(ctx)
	if err != nil {
		return fmt.Errorf("resolve master after server setup: %w", err)
	}
	ssh, err := dc.Cluster.Connect(ctx, master.IPv4+":22")
	if err != nil {
		return fmt.Errorf("establish master SSH: %w", err)
	}
	defer ssh.Close()
	dc.Cluster.MasterSSH = ssh

	kc, err := kube.New(ctx, ssh)
	if err != nil {
		return fmt.Errorf("establish master kube client: %w", err)
	}
	defer kc.Close()
	dc.Cluster.MasterKube = kc

	// Ensure the app namespace exists exactly once, up front — every
	// downstream k8s write (per-service secrets, workloads, ingress, crons)
	// assumes it's there. Without this, the first writer races and fails
	// with "namespaces not found".
	names, err := dc.Cluster.Names()
	if err != nil {
		return err
	}
	if err := kc.EnsureNamespace(ctx, names.KubeNamespace()); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}

	// Registry pull credentials must land before Services/Crons — kubelet
	// reads imagePullSecrets from the pod's namespace at first image pull.
	if err := Registries(ctx, dc, live, cfg); err != nil {
		return fmt.Errorf("registries: %w", err)
	}

	if err := Firewall(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Volumes(ctx, dc, live, cfg); err != nil {
		return err
	}
	secretValues, err := Secrets(ctx, dc, live, cfg)
	if err != nil {
		return err
	}

	storageCreds, err := Storage(ctx, dc, live, cfg)
	if err != nil {
		return err
	}

	// Build unified sources for $VAR resolution and per-service secret storage.
	sources := mergeSources(secretValues, storageCreds)

	if err := Services(ctx, dc, live, cfg, sources); err != nil {
		return err
	}
	if err := Crons(ctx, dc, live, cfg, sources); err != nil {
		return err
	}

	// Workloads have moved. Now safe to drain + delete orphan servers.
	if err := ServersRemoveOrphans(ctx, dc, live, cfg); err != nil {
		return err
	}

	// Orphan firewalls swept AFTER ServersRemoveOrphans: DeleteServer
	// detached each firewall as part of its teardown contract, so by the
	// time we reach here any firewall that fell out of the desired set has
	// zero attached resources and DeleteFirewall succeeds. Running this
	// earlier — the previous inline placement inside Firewall() — meant
	// Hetzner correctly rejected delete with resource_in_use.
	if err := FirewallRemoveOrphans(ctx, dc, live, cfg); err != nil {
		return err
	}

	if err := DNS(ctx, dc, live, cfg); err != nil {
		return err
	}

	// Verify DNS propagation before ACME — warn if domains don't resolve yet.
	if len(cfg.Domains) > 0 {
		verifyDNSPropagation(ctx, dc, cfg)
	}

	return Ingress(ctx, dc, live, cfg)
}
