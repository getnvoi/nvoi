package hetzner

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// InfraProvider satisfaction. The orchestration-heavy methods (Bootstrap /
// LiveSnapshot / TeardownOrphans / Teardown) are stubbed during this commit
// — the next commits in the refactor relocate the convergence logic from
// internal/reconcile/{servers,firewall,volumes}.go and pkg/core/{compute,
// firewall,volume}.go into private helpers in this package, then wire the
// stubs to call them. The small methods (IngressBinding, HasPublicIngress,
// ConsumesBlocks, ValidateConfig, NodeShell, Close, ListResources,
// ValidateCredentials) ship working today — they delegate to the existing
// concrete *Client surface (ListServers / GetPrivateIP / etc.).
//
// Lifecycle: cachedShell holds the SSH connection Bootstrap dialed for the
// kube tunnel. NodeShell returns it (avoids a duplicate dial). Close()
// tears it down. On the dispatch path (NodeShell called without a
// preceding Bootstrap), the cache is nil and NodeShell dials fresh.

// cachedShell stays nil until Bootstrap dials — then NodeShell returns the
// same connection. shellMu serializes lazy NodeShell dials on the dispatch
// path so two `nvoi ssh` invocations on the same provider receiver don't
// race the cache.
var (
	hetznerCacheMu sync.Mutex
)

func (c *Client) cachedShell() utils.SSHClient {
	hetznerCacheMu.Lock()
	defer hetznerCacheMu.Unlock()
	return c.shell
}

func (c *Client) setCachedShell(s utils.SSHClient) {
	hetznerCacheMu.Lock()
	defer hetznerCacheMu.Unlock()
	c.shell = s
}

// Bootstrap converges Hetzner infra to a working k3s cluster, returning
// a kube client tunneled through SSH to the master.
//
// Order matches the legacy reconcile flow exactly so the equivalence test
// passes byte-for-byte:
//
//  1. Servers: masters first, then workers (each step does swap + k3s
//     install + node label).
//  2. Firewalls: master gets public ports (80/443 + 22), workers get
//     base only (SSH + internal).
//  3. Volumes: create + SSH-mount on the volume's pinned server.
//  4. Master SSH: dial once (deploy-user / pubkey), cache as NodeShell,
//     build *kube.Client tunneled through it, return.
//
// All orchestration is owned by this package — no pkg/core delegation.
// This is the architectural shape #47 mandates: providers own their
// convergence end-to-end. The reconciler treats Bootstrap as opaque.
// Connect attaches to existing Hetzner infra. READ-ONLY: looks up the
// master via labels, dials SSH, builds the kube tunnel. Returns
// ErrNotBootstrapped when no master server matches the cluster labels
// (callers distinguish via errors.Is). No EnsureServer / EnsureFirewall
// / EnsureVolume calls — drift is NEVER reconciled here. Drift
// reconciliation lives in Bootstrap.
func (c *Client) Connect(ctx context.Context, dc *provider.BootstrapContext) (*kube.Client, error) {
	// Test scaffolding pre-injects MasterKube (KubeFake) so the invariant
	// suite can exercise the full path without an SSH-tunneled apiserver.
	if dc.MasterKube != nil {
		return dc.MasterKube, nil
	}

	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return nil, err
	}

	shell, err := c.dialSSH(ctx, dc, master.IPv4+":22")
	if err != nil {
		return nil, fmt.Errorf("hetzner.Connect dial master %s: %w", master.IPv4, err)
	}
	c.setCachedShell(shell)

	kc, err := kube.New(ctx, shell)
	if err != nil {
		return nil, fmt.Errorf("hetzner.Connect kube tunnel: %w", err)
	}
	return kc, nil
}

// Bootstrap converges Hetzner infra to the desired state, then tail-
// calls Connect to attach. WRITE: creates missing servers/firewall/
// volumes, reconciles firewall attachments, applies firewall rules,
// installs k3s. Idempotent (existing resources are lookup-only) but
// drift IS reconciled. Distinct from Connect, which never mutates.
func (c *Client) Bootstrap(ctx context.Context, dc *provider.BootstrapContext) (*kube.Client, error) {
	cfg := dc.Cfg

	// Servers — masters first, then workers (k3s join requires a running master).
	// Builders are excluded deliberately: they are provisioned out-of-band by
	// ProvisionBuilders on the operator's machine BEFORE this Bootstrap runs
	// on the builder (see pkg/provider/CLAUDE.md for the dispatch order).
	// Bootstrap here converges only the k8s cluster.
	masters, workers, _ := splitServers(cfg.ServerDefs())
	var masterShell utils.SSHClient
	for _, s := range append(masters, workers...) {
		shell, err := c.provisionServer(ctx, dc, s, masterShell)
		if err != nil {
			return nil, err
		}
		// Hold master SSH so workers can reuse it for k3s join. Worker
		// per-iteration shells close immediately after join.
		if s.Role != "worker" && masterShell == nil {
			masterShell = shell
		} else if shell != masterShell {
			_ = shell.Close()
		}
	}
	// Master shell from provisioning is throwaway — Connect (called below)
	// re-dials and caches the canonical shell. The extra dial is ~100ms
	// on production Hetzner; mock SSH is free in tests.
	if masterShell != nil {
		_ = masterShell.Close()
	}

	// Firewalls — always reconcile, rules derived from config state.
	// Caddy mode (domains set, no tunnel): 80/443 auto-open on master.
	// Tunnel mode or no domains: master gets SSH + internal only.
	// Workers always get base rules regardless.
	publicRules, err := provider.FirewallAllowList(ctx, cfg)
	if err != nil {
		return nil, err
	}
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return nil, err
	}
	if len(masters) > 0 {
		if err := c.applyFirewall(ctx, dc, names.MasterFirewall(), publicRules); err != nil {
			return nil, err
		}
	}
	if len(workers) > 0 {
		if err := c.applyFirewall(ctx, dc, names.WorkerFirewall(), nil); err != nil {
			return nil, err
		}
	}

	// Volumes — create + mount on each volume's pinned server.
	for _, v := range cfg.VolumeDefs() {
		if err := c.provisionVolume(ctx, dc, v); err != nil {
			return nil, err
		}
	}

	// Tail-call: same dial + kube-tunnel logic Connect uses on the CLI
	// dispatch path. Single source of truth for the attach half.
	return c.Connect(ctx, dc)
}

// provisionServer is the per-server orchestration that used to live in
// pkg/core/compute.go::ComputeSet. Inlined here so the provider owns its
// full convergence path without importing pkg/core.
//
// Returns the SSH connection to the new server. Master returns its own
// connection (used for the kube tunnel); workers return a transient
// connection used only for k3s join (caller closes).
func (c *Client) provisionServer(ctx context.Context, dc *provider.BootstrapContext, s provider.ServerSpec, masterShell utils.SSHClient) (utils.SSHClient, error) {
	out := dc.Output
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return nil, err
	}

	pubKey, err := utils.DerivePublicKey(dc.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}

	serverName := names.Server(s.Name)
	userData, err := infra.RenderCloudInit(strings.TrimSpace(pubKey), serverName)
	if err != nil {
		return nil, err
	}

	labels := names.Labels()
	role := utils.RoleMaster
	if s.Role == utils.RoleWorker {
		role = utils.RoleWorker
	}
	labels["role"] = role

	out.Command("instance", "set", serverName, "role", role)

	// Per-server region override (e.g. multi-region clusters); fall back
	// to the deploy-wide credential.
	srv, err := c.EnsureServer(ctx, provider.CreateServerRequest{
		Name:         serverName,
		ServerType:   s.Type,
		Image:        utils.DefaultImage,
		Location:     s.Region,
		UserData:     userData,
		FirewallName: names.FirewallForRole(role),
		NetworkName:  names.Network(),
		DiskGB:       s.Disk,
		Labels:       labels,
	})
	if err != nil {
		return nil, err
	}

	if srv.PrivateIP == "" {
		ip, err := c.GetPrivateIP(ctx, srv.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve private IP for %s: %w", srv.Name, err)
		}
		if ip == "" {
			return nil, fmt.Errorf("server %s has no private IP — private network may not be attached", srv.Name)
		}
		srv.PrivateIP = ip
	}

	if err := infra.ClearKnownHost(srv.IPv4 + ":22"); err != nil {
		if !errors.Is(err, infra.ErrNoKnownHost) {
			out.Warning(fmt.Sprintf("clear known host %s: %s", srv.IPv4, err))
		}
	}

	out.Progress(fmt.Sprintf("waiting for SSH on %s", srv.IPv4))
	var ssh utils.SSHClient
	sshCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := utils.Poll(sshCtx, 2*time.Second, 5*time.Minute, func() (bool, error) {
		conn, err := c.dialSSH(ctx, dc, srv.IPv4+":22")
		if err != nil {
			if errors.Is(err, infra.ErrHostKeyChanged) {
				return false, err
			}
			if errors.Is(err, infra.ErrAuthFailed) {
				return false, err
			}
			return false, nil
		}
		ssh = conn
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("SSH not reachable on %s: %w", srv.IPv4, err)
	}
	out.Success("SSH ready")

	out.Progress("ensuring swap")
	if err := infra.EnsureSwap(ctx, ssh, out.Writer()); err != nil {
		out.Warning(fmt.Sprintf("swap: %s", err))
	} else {
		out.Success("swap ready")
	}

	srvNode := infra.Node{PublicIP: srv.IPv4, PrivateIP: srv.PrivateIP}

	if s.Role == utils.RoleWorker {
		// Worker join needs the master's SSH for the join token.
		joinShell := masterShell
		if joinShell == nil {
			master, err := c.findMaster(ctx, dc)
			if err != nil {
				return nil, err
			}
			out.Progress(fmt.Sprintf("joining cluster via master %s", master.IPv4))
			joinShell, err = c.dialSSH(ctx, dc, master.IPv4+":22")
			if err != nil {
				return nil, fmt.Errorf("ssh master for worker join: %w", err)
			}
			defer joinShell.Close()
		}

		master, err := c.findMaster(ctx, dc)
		if err != nil {
			return nil, err
		}
		masterNode := infra.Node{PublicIP: master.IPv4, PrivateIP: master.PrivateIP}
		if err := infra.JoinK3sWorker(ctx, joinShell, ssh, srvNode, masterNode, out.Writer()); err != nil {
			return nil, fmt.Errorf("k3s worker join: %w", err)
		}
		out.Success("joined cluster")

		if err := labelNodeViaSSH(ctx, joinShell, serverName, s.Name); err != nil {
			return nil, fmt.Errorf("label node: %w", err)
		}
	} else {
		out.Progress("installing k3s master")
		if err := infra.InstallK3sMaster(ctx, ssh, srvNode, out.Writer()); err != nil {
			return nil, fmt.Errorf("k3s master: %w", err)
		}
		out.Success("k3s master ready")

		if err := labelNodeViaSSH(ctx, ssh, serverName, s.Name); err != nil {
			return nil, fmt.Errorf("label node: %w", err)
		}
	}

	out.Success(fmt.Sprintf("node labeled %s=%s", utils.LabelNvoiRole, s.Name))
	out.Success(fmt.Sprintf("%s %s (private: %s)", serverName, srv.IPv4, srv.PrivateIP))
	return ssh, nil
}

// applyFirewall is the per-firewall set step. Wrap ReconcileFirewallRules
// with the same Output events the legacy pkg/core/firewall.go::FirewallSet
// emits so the equivalence test goldens still match byte-for-byte.
func (c *Client) applyFirewall(ctx context.Context, dc *provider.BootstrapContext, name string, allowed provider.PortAllowList) error {
	out := dc.Output
	out.Command("firewall", "set", name)
	if err := c.ReconcileFirewallRules(ctx, name, allowed); err != nil {
		return fmt.Errorf("firewall set: %w", err)
	}
	if len(allowed) == 0 {
		out.Success("base rules only (SSH + internal)")
	} else {
		for _, port := range provider.SortedPorts(allowed) {
			out.Success(fmt.Sprintf("port %s → %v", port, allowed[port]))
		}
	}
	return nil
}

// provisionVolume creates the volume + SSH-mounts it on its pinned server.
// Mirror of pkg/core/volume.go::VolumeSet.
func (c *Client) provisionVolume(ctx context.Context, dc *provider.BootstrapContext, v provider.VolumeSpec) error {
	out := dc.Output
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return err
	}

	volumeName := names.Volume(v.Name)
	serverName := names.Server(v.Server)
	mountPath := names.VolumeMountPath(v.Name)

	out.Command("volume", "set", volumeName, "size", fmt.Sprintf("%dGB", v.Size), "server", serverName)

	vol, err := c.EnsureVolume(ctx, provider.CreateVolumeRequest{
		Name:       volumeName,
		ServerName: serverName,
		Size:       v.Size,
		Labels:     names.Labels(),
	})
	if err != nil {
		return err
	}

	servers, err := c.ListServers(ctx, names.Labels())
	if err != nil {
		return fmt.Errorf("list servers: %w", err)
	}
	var serverIP string
	for _, s := range servers {
		if s.Name == serverName {
			serverIP = s.IPv4
			break
		}
	}
	if serverIP == "" {
		return fmt.Errorf("server %s not found", serverName)
	}

	ssh, err := c.dialSSH(ctx, dc, serverIP+":22")
	if err != nil {
		return fmt.Errorf("ssh for volume mount: %w", err)
	}
	defer ssh.Close()

	devicePath := c.ResolveDevicePath(vol)
	if err := infra.MountVolume(ctx, ssh, devicePath, mountPath, out.Writer()); err != nil {
		return fmt.Errorf("mount: %w", err)
	}

	out.Success(fmt.Sprintf("%s → %s on %s", volumeName, mountPath, serverName))
	return nil
}

// ProvisionBuilders converges role: builder servers + their per-server
// cache volumes + the shared builder firewall. Idempotent: existing
// builders short-circuit through EnsureServer / EnsureVolume; missing
// ones are created. Runs BEFORE Bootstrap on the SSH-build dispatch path
// (see CLAUDE.md) so builders exist before the CLI fetches BuilderTargets.
// No-op (returns nil) when no role: builder servers are declared.
func (c *Client) ProvisionBuilders(ctx context.Context, dc *provider.BootstrapContext) error {
	_, _, builders := splitServers(dc.Cfg.ServerDefs())
	if len(builders) == 0 {
		return nil
	}

	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return err
	}
	// Builder firewall: base rules only (SSH + internal). Public 80/443
	// never reach a builder — the public ingress path is master-side only.
	if err := c.applyFirewall(ctx, dc, names.BuilderFirewall(), nil); err != nil {
		return err
	}
	for _, b := range builders {
		if err := c.provisionBuilder(ctx, dc, b); err != nil {
			return err
		}
	}
	return nil
}

// provisionBuilder is the per-builder orchestration. Distinct from
// provisionServer — builders are NOT k8s nodes. The sequence:
//
//  1. EnsureServer with RenderBuilderCloudInit (Docker CE, disabled,
//     daemon.json drop) + BuilderFirewall.
//  2. Wait for SSH, enable swap.
//  3. EnsureVolume for the cache, SSH-mount at BuilderCacheMountPath
//     (blkid-gated mkfs via infra.MountVolume — safe to re-run).
//  4. Enable + start docker (it was disabled by cloud-init so /var/lib/docker
//     wouldn't shadow the cache-mount) and register binfmt_misc so the
//     builder can cross-compile for any target arch.
func (c *Client) provisionBuilder(ctx context.Context, dc *provider.BootstrapContext, s provider.ServerSpec) error {
	out := dc.Output
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return err
	}

	pubKey, err := utils.DerivePublicKey(dc.SSHKey)
	if err != nil {
		return fmt.Errorf("derive public key: %w", err)
	}

	serverName := names.Server(s.Name)
	userData, err := infra.RenderBuilderCloudInit(strings.TrimSpace(pubKey), serverName)
	if err != nil {
		return err
	}

	labels := names.Labels()
	labels["role"] = utils.RoleBuilder

	out.Command("instance", "set", serverName, "role", utils.RoleBuilder)

	srv, err := c.EnsureServer(ctx, provider.CreateServerRequest{
		Name:         serverName,
		ServerType:   s.Type,
		Image:        utils.DefaultImage,
		Location:     s.Region,
		UserData:     userData,
		FirewallName: names.BuilderFirewall(),
		NetworkName:  names.Network(),
		DiskGB:       s.Disk,
		Labels:       labels,
	})
	if err != nil {
		return err
	}

	if err := infra.ClearKnownHost(srv.IPv4 + ":22"); err != nil {
		if !errors.Is(err, infra.ErrNoKnownHost) {
			out.Warning(fmt.Sprintf("clear known host %s: %s", srv.IPv4, err))
		}
	}

	out.Progress(fmt.Sprintf("waiting for SSH on %s", srv.IPv4))
	var ssh utils.SSHClient
	sshCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := utils.Poll(sshCtx, 2*time.Second, 5*time.Minute, func() (bool, error) {
		conn, err := c.dialSSH(ctx, dc, srv.IPv4+":22")
		if err != nil {
			if errors.Is(err, infra.ErrHostKeyChanged) {
				return false, err
			}
			if errors.Is(err, infra.ErrAuthFailed) {
				return false, err
			}
			return false, nil
		}
		ssh = conn
		return true, nil
	}); err != nil {
		return fmt.Errorf("SSH not reachable on %s: %w", srv.IPv4, err)
	}
	defer ssh.Close()
	out.Success("SSH ready")

	// Gate every subsequent step on cloud-init being fully applied. Ubuntu
	// opens SSH before runcmd finishes, so `systemctl enable --now docker`
	// (and the nvoi install referenced by the SSH BuildProvider) would
	// otherwise race cloud-init's apt install / curl|sh on fast networks.
	out.Progress("waiting for cloud-init")
	if err := infra.WaitCloudInit(ctx, ssh); err != nil {
		return err
	}
	out.Success("cloud-init done")

	out.Progress("ensuring swap")
	if err := infra.EnsureSwap(ctx, ssh, out.Writer()); err != nil {
		out.Warning(fmt.Sprintf("swap: %s", err))
	} else {
		out.Success("swap ready")
	}

	// Cache volume — per-builder, name-keyed so different builders never
	// contend on the same buildkit cache directory.
	cacheName := names.BuilderCacheVolume(s.Name)
	out.Command("volume", "set", cacheName, "size", fmt.Sprintf("%dGB", utils.BuilderCacheVolumeSizeGB), "server", serverName)
	vol, err := c.EnsureVolume(ctx, provider.CreateVolumeRequest{
		Name:       cacheName,
		ServerName: serverName,
		Size:       utils.BuilderCacheVolumeSizeGB,
		Labels:     labels,
	})
	if err != nil {
		return fmt.Errorf("builder cache volume: %w", err)
	}

	devicePath := c.ResolveDevicePath(vol)
	if err := infra.MountVolume(ctx, ssh, devicePath, utils.BuilderCacheMountPath, out.Writer()); err != nil {
		return fmt.Errorf("mount builder cache: %w", err)
	}

	// Docker was installed-but-disabled by cloud-init so /var/lib/docker
	// wouldn't get populated on the root disk before the cache mount took
	// effect. Now that BuilderCacheMountPath is live, enable + start it,
	// then register binfmt so this builder can produce images for any arch.
	out.Progress("enabling docker on builder")
	if _, err := ssh.Run(ctx, "sudo systemctl enable --now docker.service docker.socket"); err != nil {
		return fmt.Errorf("enable docker: %w", err)
	}
	if err := ssh.RunStream(ctx, "sudo docker run --privileged --rm tonistiigi/binfmt:latest --install all", out.Writer(), out.Writer()); err != nil {
		// binfmt failure is non-fatal — builds targeting the host's native
		// arch still succeed. Warn-and-continue mirrors the swap pattern.
		out.Warning(fmt.Sprintf("binfmt register: %s", err))
	}
	out.Success(fmt.Sprintf("%s builder ready on %s", serverName, srv.IPv4))
	return nil
}

// BuilderTargets returns every role: builder server this provider manages
// for the current cluster. Looks up by labels (role=builder). Empty slice
// when no builders are provisioned — callers feature-gate on len() == 0.
// Same ownership pattern as NodeShell: the provider owns the lookup, the
// CLI dispatcher consumes the result.
func (c *Client) BuilderTargets(ctx context.Context, dc *provider.BootstrapContext) ([]provider.BuilderTarget, error) {
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return nil, err
	}
	labels := names.Labels()
	labels["role"] = utils.RoleBuilder
	servers, err := c.ListServers(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("hetzner.BuilderTargets: %w", err)
	}
	targets := make([]provider.BuilderTarget, 0, len(servers))
	for _, s := range servers {
		targets = append(targets, provider.BuilderTarget{
			Name: s.Name,
			Host: s.IPv4,
			User: utils.DefaultUser,
		})
	}
	// Deterministic order: tests + resource-listing want stability.
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets, nil
}

// labelNodeViaSSH labels a k8s node by exec'ing kubectl on the master via
// SSH. Avoids needing a kube.Client during Bootstrap (we don't have one
// until Bootstrap finishes).
func labelNodeViaSSH(ctx context.Context, ssh utils.SSHClient, nodeName, role string) error {
	cmd := fmt.Sprintf("sudo kubectl --kubeconfig=%s label node %s %s=%s --overwrite",
		utils.KubeconfigPath, nodeName, utils.LabelNvoiRole, role)
	_, err := ssh.Run(ctx, cmd)
	return err
}

// LiveSnapshot returns Hetzner's view of the live cluster — server,
// volume, and firewall short-names (cluster prefix stripped) for orphan
// detection. Returns nil when no servers exist yet (first deploy).
func (c *Client) LiveSnapshot(ctx context.Context, dc *provider.BootstrapContext) (*provider.LiveSnapshot, error) {
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return nil, err
	}
	prefix := names.Base() + "-"
	strip := func(s string) string {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			return s[len(prefix):]
		}
		return s
	}

	servers, err := c.ListServers(ctx, names.Labels())
	if err != nil {
		return nil, fmt.Errorf("hetzner.LiveSnapshot list servers: %w", err)
	}
	if len(servers) == 0 {
		return nil, nil // first deploy
	}

	snap := &provider.LiveSnapshot{ServerDisk: map[string]int{}}
	for _, s := range servers {
		short := strip(s.Name)
		snap.Servers = append(snap.Servers, short)
		if s.DiskGB > 0 {
			snap.ServerDisk[short] = s.DiskGB
		}
	}

	if vols, err := c.ListVolumes(ctx, names.Labels()); err == nil {
		for _, v := range vols {
			snap.Volumes = append(snap.Volumes, strip(v.Name))
		}
	}

	if fws, err := c.ListAllFirewalls(ctx); err == nil {
		for _, fw := range fws {
			if len(fw.Name) > len(prefix) && fw.Name[:len(prefix)] == prefix {
				snap.Firewalls = append(snap.Firewalls, fw.Name)
			}
		}
	}

	sort.Strings(snap.Servers)
	sort.Strings(snap.Volumes)
	sort.Strings(snap.Firewalls)
	return snap, nil
}

// TeardownOrphans removes Hetzner resources in live but not in the desired
// config. Order:
//
//  1. Drain + delete orphan servers (workloads must have moved already —
//     reconcile calls this after Services/Crons).
//  2. Sweep orphan firewalls AFTER servers (DeleteServer detached them, so
//     DeleteFirewall succeeds — Hetzner rejects with resource_in_use
//     otherwise).
//  3. Best-effort orphan volume delete (warn-on-fail; next deploy retries).
func (c *Client) TeardownOrphans(ctx context.Context, dc *provider.BootstrapContext) error {
	cfg := dc.Cfg
	out := dc.Output
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return err
	}

	// Provider does its own live-state lookup (D3 dropped the live param
	// from the interface — keeps reconcile free of provider-shape data).
	live, err := c.LiveSnapshot(ctx, dc)
	if err != nil {
		return fmt.Errorf("hetzner.TeardownOrphans live: %w", err)
	}
	if live == nil {
		return nil
	}

	desiredServers := serverNameSet(cfg.ServerDefs())
	for _, short := range live.Servers {
		if desiredServers[short] {
			continue
		}
		if err := c.drainAndDeleteServer(ctx, dc, names, short); err != nil {
			return err
		}
	}

	// Firewall orphan sweep — runs unconditionally. The previous gate
	// (`if cfg.FirewallRules() > 0`) skipped the sweep entirely when
	// the user had no explicit rules in cfg, leaving worker-fw or
	// builder-fw orphans on the account forever after a yaml change
	// dropped a role. The desired set is derived from cfg.ServerDefs()
	// alone — no rules, no servers of role X → role X's firewall is
	// not desired and gets swept.
	desiredFW := map[string]bool{}
	masters, workers, builders := splitServers(cfg.ServerDefs())
	if len(masters) > 0 {
		desiredFW[names.MasterFirewall()] = true
	}
	if len(workers) > 0 {
		desiredFW[names.WorkerFirewall()] = true
	}
	if len(builders) > 0 {
		desiredFW[names.BuilderFirewall()] = true
	}
	c.sweepFirewalls(ctx, out, names.Base()+"-", desiredFW)

	// Desired volumes = user-declared + synthetic per-builder caches.
	// Orphan sweep must preserve cache volumes attached to declared builders
	// (they are provider-managed, not user-declared in volumes:). Reuses
	// `builders` from the firewall sweep above.
	desiredVols := volumeNameSet(cfg.VolumeDefs())
	for _, b := range builders {
		desiredVols[names.BuilderCacheVolumeShort(b.Name)] = true
	}
	for _, short := range live.Volumes {
		if desiredVols[short] {
			continue
		}
		if err := c.unmountAndDeleteVolume(ctx, dc, names, short); err != nil {
			out.Warning(fmt.Sprintf("orphan volume %s not removed: %s", short, err))
		}
	}
	return nil
}

// Teardown hard-nukes every Hetzner resource matching this cluster's
// labels. Backs `bin/destroy`. Order: optional volumes → servers
// (workers first, then master) → firewalls → network. With
// deleteVolumes=false, persistent volumes are detached on server delete
// but preserved (nvoi reattaches on next deploy).
func (c *Client) Teardown(ctx context.Context, dc *provider.BootstrapContext, deleteVolumes bool) error {
	cfg := dc.Cfg
	out := dc.Output
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return err
	}
	var firstErr error
	collect := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	masters, workers, builders := splitServers(cfg.ServerDefs())

	if deleteVolumes {
		for _, v := range cfg.VolumeDefs() {
			collect(c.unmountAndDeleteVolume(ctx, dc, names, v.Name))
		}
	}
	// Builder cache volumes are provider-synthesized (not in cfg.VolumeDefs)
	// and are ALWAYS deleted on teardown regardless of --delete-volumes —
	// nvoi-owned cache, never user data, no reason to preserve.
	for _, b := range builders {
		collect(c.unmountAndDeleteVolume(ctx, dc, names, names.BuilderCacheVolumeShort(b.Name)))
	}

	// Builders first — they have no k8s state to drain and their firewall
	// is disjoint from master/worker, so they tear down independently.
	for _, s := range builders {
		collect(c.drainAndDeleteServer(ctx, dc, names, s.Name))
	}
	for _, s := range workers {
		collect(c.drainAndDeleteServer(ctx, dc, names, s.Name))
	}
	for _, s := range masters {
		collect(c.drainAndDeleteServer(ctx, dc, names, s.Name))
	}

	c.sweepFirewalls(ctx, out, names.Base()+"-", nil) // nil = delete all matching prefix

	out.Command("network", "delete", names.Network())
	if err := c.DeleteNetwork(ctx, names.Network()); err != nil {
		collect(fmt.Errorf("network delete: %w", err))
	} else {
		out.Success(names.Network() + " deleted")
	}
	return firstErr
}

// drainAndDeleteServer drains the k8s node (if MasterKube reachable) then
// hands off to DeleteServer (which detaches firewall, detaches volumes,
// then terminates).
func (c *Client) drainAndDeleteServer(ctx context.Context, dc *provider.BootstrapContext, names *utils.Names, short string) error {
	out := dc.Output
	serverName := names.Server(short)

	// Drain via cached SSH (Bootstrap sets it for the deploy path; nil
	// during teardown means we skip drain — the server is being destroyed
	// anyway, k8s state dies with it).
	if shell := c.cachedShell(); shell != nil {
		out.Command("node", "drain", serverName)
		// Best-effort drain via kubectl over SSH; deletion proceeds even
		// if drain fails (the node is going away).
		drainCmd := fmt.Sprintf("sudo kubectl --kubeconfig=%s drain %s --ignore-daemonsets --delete-emptydir-data --force --grace-period=30 --timeout=60s",
			utils.KubeconfigPath, serverName)
		if _, err := shell.Run(ctx, drainCmd); err != nil {
			out.Warning(fmt.Sprintf("drain %s: %s", serverName, err))
		}
	}

	out.Command("instance", "delete", serverName)
	servers, _ := c.ListServers(ctx, names.Labels())
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
	if err := c.DeleteServer(ctx, provider.DeleteServerRequest{
		Name:   serverName,
		Labels: names.Labels(),
	}); err != nil {
		return err
	}
	out.Success(serverName + " deleted")
	return nil
}

// unmountAndDeleteVolume unmounts on every server (best effort) then deletes.
func (c *Client) unmountAndDeleteVolume(ctx context.Context, dc *provider.BootstrapContext, names *utils.Names, short string) error {
	out := dc.Output
	volumeName := names.Volume(short)
	mountPath := names.VolumeMountPath(short)

	out.Command("volume", "delete", volumeName)
	volumes, _ := c.ListVolumes(ctx, names.Labels())
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

	servers, err := c.ListServers(ctx, names.Labels())
	if err == nil {
		for _, s := range servers {
			ssh, err := c.dialSSH(ctx, dc, s.IPv4+":22")
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
	if err := c.DeleteVolume(ctx, volumeName); err != nil {
		return err
	}
	out.Success(volumeName + " deleted")
	return nil
}

// sweepFirewalls deletes every firewall matching prefix that isn't in
// desired. Best-effort — failures emit warnings and the next deploy
// retries. desired=nil means delete everything matching prefix.
func (c *Client) sweepFirewalls(ctx context.Context, out provider.EventSink, prefix string, desired map[string]bool) {
	all, err := c.ListAllFirewalls(ctx)
	if err != nil {
		out.Warning(fmt.Sprintf("list firewalls for orphan sweep: %s", err))
		return
	}
	for _, fw := range all {
		if len(fw.Name) <= len(prefix) || fw.Name[:len(prefix)] != prefix {
			continue
		}
		if desired[fw.Name] {
			continue
		}
		out.Command("firewall", "delete", fw.Name)
		if err := c.DeleteFirewall(ctx, fw.Name); err != nil {
			out.Warning(fmt.Sprintf("orphan firewall %s not removed: %s", fw.Name, err))
			continue
		}
		out.Success(fw.Name + " deleted")
	}
}

// splitServers returns ServerSpecs grouped by role, sorted within each
// group. Three-way split: role: builder is the third category (added in
// #57) and is NEVER iterated by Bootstrap — builders are out-of-band to
// the k8s cluster and are provisioned by ProvisionBuilders on the
// operator's machine BEFORE the deploy is dispatched into a builder.
// Any role except "worker" / "builder" (including the empty default)
// falls into masters, matching the validator's role enum.
func splitServers(defs []provider.ServerSpec) (masters, workers, builders []provider.ServerSpec) {
	for _, s := range defs {
		switch s.Role {
		case utils.RoleWorker:
			workers = append(workers, s)
		case utils.RoleBuilder:
			builders = append(builders, s)
		default:
			masters = append(masters, s)
		}
	}
	sort.Slice(masters, func(i, j int) bool { return masters[i].Name < masters[j].Name })
	sort.Slice(workers, func(i, j int) bool { return workers[i].Name < workers[j].Name })
	sort.Slice(builders, func(i, j int) bool { return builders[i].Name < builders[j].Name })
	return
}

func serverNameSet(defs []provider.ServerSpec) map[string]bool {
	m := make(map[string]bool, len(defs))
	for _, s := range defs {
		m[s.Name] = true
	}
	return m
}

func volumeNameSet(defs []provider.VolumeSpec) map[string]bool {
	m := make(map[string]bool, len(defs))
	for _, v := range defs {
		m[v.Name] = true
	}
	return m
}

// IngressBinding returns the master's public IPv4 wrapped in a DNS-A hint.
// Cloudflare may proxy and rewrite to its own representation; non-proxying
// providers write a plain A record.
func (c *Client) IngressBinding(ctx context.Context, dc *provider.BootstrapContext, _ provider.ServiceTarget) (provider.IngressBinding, error) {
	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return provider.IngressBinding{}, err
	}
	return provider.IngressBinding{DNSType: "A", DNSTarget: master.IPv4}, nil
}

// HasPublicIngress: every Hetzner server has a routable public IPv4. Caddy
// can bind hostPort:80/443 on the master node for ingress termination.
func (c *Client) HasPublicIngress() bool { return true }

// ConsumesBlocks: Hetzner reads the YAML blocks the existing IaaS reconcile
// already consumes. Validator rejects any other top-level provider block
// (e.g. `sandbox:` under hetzner) with an actionable error.
func (c *Client) ConsumesBlocks() []string {
	return []string{"servers", "firewall", "volumes"}
}

// ValidateConfig enforces Hetzner-specific invariants beyond the generic
// validator. The single hard rule today: `disk:` is illegal because
// Hetzner root disk is fixed per server type — there is no API to set or
// resize it. The generic validator already rejects it for hetzner; we
// repeat the check defensively here so the contract lives next to the
// implementation, not three layers up.
func (c *Client) ValidateConfig(cfg provider.ProviderConfigView) error {
	for _, s := range cfg.ServerDefs() {
		if s.Disk > 0 {
			return fmt.Errorf("servers.%s.disk: hetzner root disk is fixed per server type — remove disk: from config", s.Name)
		}
	}
	return nil
}

// NodeShell returns an SSH client to the master. Two paths:
//
//  1. Deploy path: Bootstrap already dialed and cached. Reuse — avoids a
//     second connection to the same host.
//  2. Dispatch path (`nvoi ssh` without a prior deploy in this process):
//     find master by label, dial fresh. Caller owns Close via Cluster.
//
// Returns (nil, error) on hard failures. Returns (nil, nil) is reserved
// for providers that genuinely don't expose host shell — Hetzner always
// does, so we never return (nil, nil).
func (c *Client) NodeShell(ctx context.Context, dc *provider.BootstrapContext) (utils.SSHClient, error) {
	if s := c.cachedShell(); s != nil {
		return s, nil
	}
	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return nil, fmt.Errorf("hetzner.NodeShell: %w", err)
	}
	conn, err := c.dialSSH(ctx, dc, master.IPv4+":22")
	if err != nil {
		return nil, fmt.Errorf("hetzner.NodeShell dial %s: %w", master.IPv4, err)
	}
	c.setCachedShell(conn)
	return conn, nil
}

// SSHToNode dials SSH to a specific node identified by its
// YAML-declared server short name. Looks up the server by its full
// cluster-qualified name (nvoi-{app}-{env}-{serverName}) and dials
// its public IPv4 with the same key NodeShell uses. Returns
// ErrNotBootstrapped when the named server doesn't exist.
//
// Unlike NodeShell, the connection is NOT cached on the provider —
// callers that need repeated access to non-master nodes should cache
// their own handle (or re-dial, since the cost is a single SSH
// handshake). Caller owns Close().
func (c *Client) SSHToNode(ctx context.Context, dc *provider.BootstrapContext, serverName string) (utils.SSHClient, error) {
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return nil, err
	}
	fullName := names.Server(serverName)
	srv, err := c.getServerByName(ctx, fullName)
	if err != nil {
		return nil, fmt.Errorf("hetzner.SSHToNode %s: %w", serverName, err)
	}
	if srv == nil {
		return nil, fmt.Errorf("hetzner.SSHToNode %s: %w", serverName, provider.ErrNotBootstrapped)
	}
	if srv.IPv4 == "" {
		return nil, fmt.Errorf("hetzner.SSHToNode %s: server has no public IPv4", serverName)
	}
	conn, err := c.dialSSH(ctx, dc, srv.IPv4+":22")
	if err != nil {
		return nil, fmt.Errorf("hetzner.SSHToNode dial %s: %w", srv.IPv4, err)
	}
	return conn, nil
}

// Close releases the cached SSH if Bootstrap (or NodeShell's cold path)
// established one. Idempotent.
func (c *Client) Close() error {
	hetznerCacheMu.Lock()
	s := c.shell
	c.shell = nil
	hetznerCacheMu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}

// findMaster locates the master server by cluster labels. Returns
// (master, nil) on hit, (nil, provider.ErrNotBootstrapped) when no
// matching server exists, (nil, wrappedErr) on API failure. Callers
// use errors.Is(err, provider.ErrNotBootstrapped) to distinguish
// "cluster absent" from "lookup failed" — same pattern as os.IsNotExist
// / sql.ErrNoRows.
func (c *Client) findMaster(ctx context.Context, dc *provider.BootstrapContext) (*provider.Server, error) {
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return nil, err
	}
	labels := names.Labels()
	labels["role"] = "master"
	masters, err := c.ListServers(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("find master: %w", err)
	}
	if len(masters) == 0 {
		return nil, provider.ErrNotBootstrapped
	}
	master := masters[0]
	if master.PrivateIP == "" {
		ip, err := c.GetPrivateIP(ctx, master.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve master private IP: %w", err)
		}
		if ip == "" {
			return nil, fmt.Errorf("master %s has no private IP — private network may not be attached", master.Name)
		}
		master.PrivateIP = ip
	}
	return master, nil
}

// dialSSH opens an SSH connection respecting BootstrapContext.SSHDial
// when set (tests inject a closure returning MockSSH); production
// providers fall back to infra.ConnectSSH with the operator's pubkey.
// Centralized so every helper in this file dials the same way.
func (c *Client) dialSSH(ctx context.Context, dc *provider.BootstrapContext, addr string) (utils.SSHClient, error) {
	if dc.SSHDial != nil {
		return dc.SSHDial(ctx, addr)
	}
	return infra.ConnectSSH(ctx, addr, utils.DefaultUser, dc.SSHKey)
}

// Compile-time check that *Client satisfies provider.InfraProvider.
var _ provider.InfraProvider = (*Client)(nil)

// kube import is load-bearing for the Bootstrap return type.
var _ = (*kube.Client)(nil)
