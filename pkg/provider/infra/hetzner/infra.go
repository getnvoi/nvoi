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
func (c *Client) Bootstrap(ctx context.Context, dc *provider.BootstrapContext) (*kube.Client, error) {
	cfg := dc.Cfg

	// Servers — masters first, then workers (k3s join requires a running master).
	masters, workers := splitServers(cfg.ServerDefs())
	var masterShell utils.SSHClient
	for _, s := range append(masters, workers...) {
		shell, err := c.provisionServer(ctx, dc, s, masterShell)
		if err != nil {
			return nil, err
		}
		// Capture master SSH on first iteration (masters come first); workers
		// reuse it for k3s join. Closed via Close() at end of deploy.
		if s.Role != "worker" && masterShell == nil {
			masterShell = shell
		} else if shell != masterShell {
			// Worker per-iteration shells are short-lived — close once we're done.
			_ = shell.Close()
		}
	}

	// Firewalls — same shape as legacy reconcile.Firewall step.
	if rules := cfg.FirewallRules(); len(rules) > 0 {
		publicRules, err := provider.ResolveFirewallArgs(ctx, rules)
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
	}

	// Volumes — create + mount on each volume's pinned server.
	for _, v := range cfg.VolumeDefs() {
		if err := c.provisionVolume(ctx, dc, v); err != nil {
			return nil, err
		}
	}

	// Cache the master SSH as NodeShell. If masterShell is somehow nil
	// (no master in the loop above — would have failed earlier), find +
	// dial fresh. Build *kube.Client tunneled through it.
	if masterShell == nil {
		master, err := c.findMaster(ctx, dc)
		if err != nil {
			return nil, fmt.Errorf("hetzner.Bootstrap: %w", err)
		}
		masterShell, err = c.dialSSH(ctx, dc, master.IPv4+":22")
		if err != nil {
			return nil, fmt.Errorf("hetzner.Bootstrap dial master %s: %w", master.IPv4, err)
		}
	}
	c.setCachedShell(masterShell)

	// Test scaffolding pre-injects MasterKube (KubeFake from convergeDC)
	// so the equivalence/invariant suite can exercise the full Deploy
	// path without an SSH-tunneled apiserver. Production deploys leave
	// it nil and we build the real tunneled client below.
	if dc.MasterKube != nil {
		return dc.MasterKube, nil
	}
	kc, err := kube.New(ctx, masterShell)
	if err != nil {
		return nil, fmt.Errorf("hetzner.Bootstrap kube tunnel: %w", err)
	}
	return kc, nil
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
	role := "master"
	if s.Role == "worker" {
		role = "worker"
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
		if !strings.Contains(err.Error(), "no known host") {
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

	if s.Role == "worker" {
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
func (c *Client) TeardownOrphans(ctx context.Context, dc *provider.BootstrapContext, live *provider.LiveSnapshot) error {
	if live == nil {
		return nil
	}
	cfg := dc.Cfg
	out := dc.Output
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return err
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

	if rules := cfg.FirewallRules(); len(rules) > 0 {
		desiredFW := map[string]bool{}
		masters, workers := splitServers(cfg.ServerDefs())
		if len(masters) > 0 {
			desiredFW[names.MasterFirewall()] = true
		}
		if len(workers) > 0 {
			desiredFW[names.WorkerFirewall()] = true
		}
		c.sweepFirewalls(ctx, out, names.Base()+"-", desiredFW)
	}

	desiredVols := volumeNameSet(cfg.VolumeDefs())
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

	if deleteVolumes {
		for _, v := range cfg.VolumeDefs() {
			collect(c.unmountAndDeleteVolume(ctx, dc, names, v.Name))
		}
	}

	masters, workers := splitServers(cfg.ServerDefs())
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
// group. Mirrors internal/reconcile/helpers.go::SplitServers.
func splitServers(defs []provider.ServerSpec) (masters, workers []provider.ServerSpec) {
	for _, s := range defs {
		if s.Role == "worker" {
			workers = append(workers, s)
		} else {
			masters = append(masters, s)
		}
	}
	sort.Slice(masters, func(i, j int) bool { return masters[i].Name < masters[j].Name })
	sort.Slice(workers, func(i, j int) bool { return workers[i].Name < workers[j].Name })
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
	conn, err := infra.ConnectSSH(ctx, master.IPv4+":22", utils.DefaultUser, dc.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("hetzner.NodeShell dial %s: %w", master.IPv4, err)
	}
	c.setCachedShell(conn)
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

// findMaster locates the master server by cluster labels using the
// existing ListServers + GetPrivateIP surface. Replaces pkg/core's
// FindMaster, which depended on the doomed ComputeProvider interface.
// Private to the package — IngressBinding / NodeShell are the only
// callers.
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
		return nil, fmt.Errorf("no master server found for %s/%s", dc.App, dc.Env)
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
