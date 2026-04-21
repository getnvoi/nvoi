package scaleway

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

// InfraProvider satisfaction. Same staging pattern as Hetzner / AWS —
// small methods ship working today; orchestration-heavy methods stub
// returning ErrNotImplemented until the reconcile rewrite lands.

var scalewayCacheMu sync.Mutex

func (c *Client) cachedShell() utils.SSHClient {
	scalewayCacheMu.Lock()
	defer scalewayCacheMu.Unlock()
	return c.shell
}

func (c *Client) setCachedShell(s utils.SSHClient) {
	scalewayCacheMu.Lock()
	defer scalewayCacheMu.Unlock()
	c.shell = s
}

// Bootstrap converges Scaleway infra to a working k3s cluster, returning
// a kube client tunneled through SSH to the master. Same shape as
// Hetzner / AWS — servers (masters then workers, k3s install/join,
// node label) → firewalls → volumes → master SSH + kube tunnel.
// Scaleway-specific networking (private network attachment) handled
// inside EnsureServer.
func (c *Client) Bootstrap(ctx context.Context, dc *provider.BootstrapContext) (*kube.Client, error) {
	cfg := dc.Cfg

	// Builders out-of-band — ProvisionBuilders runs separately. Bootstrap
	// here touches only the k8s cluster (masters + workers).
	masters, workers, _ := splitServers(cfg.ServerDefs())
	var masterShell utils.SSHClient
	for _, s := range append(masters, workers...) {
		shell, err := c.provisionServer(ctx, dc, s, masterShell)
		if err != nil {
			return nil, err
		}
		if s.Role != "worker" && masterShell == nil {
			masterShell = shell
		} else if shell != masterShell {
			_ = shell.Close()
		}
	}

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

	for _, v := range cfg.VolumeDefs() {
		if err := c.provisionVolume(ctx, dc, v); err != nil {
			return nil, err
		}
	}
	if masterShell != nil {
		_ = masterShell.Close()
	}

	return c.Connect(ctx, dc)
}

// Connect attaches to existing Scaleway infra. READ-ONLY: lookup
// master by label, dial SSH, build kube tunnel. Returns
// provider.ErrNotBootstrapped when no master found (callers distinguish
// via errors.Is). No EnsureServer / EnsureFirewall / EnsureVolume — drift
// reconciliation is Bootstrap's job.
func (c *Client) Connect(ctx context.Context, dc *provider.BootstrapContext) (*kube.Client, error) {
	if dc.MasterKube != nil {
		return dc.MasterKube, nil
	}
	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return nil, err
	}
	shell, err := c.dialSSH(ctx, dc, master.IPv4+":22")
	if err != nil {
		return nil, fmt.Errorf("scaleway.Connect dial master %s: %w", master.IPv4, err)
	}
	c.setCachedShell(shell)
	kc, err := kube.New(ctx, shell)
	if err != nil {
		return nil, fmt.Errorf("scaleway.Connect kube tunnel: %w", err)
	}
	return kc, nil
}

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
		return nil, fmt.Errorf("scaleway.LiveSnapshot list servers: %w", err)
	}
	if len(servers) == 0 {
		return nil, nil
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

func (c *Client) TeardownOrphans(ctx context.Context, dc *provider.BootstrapContext) error {
	cfg := dc.Cfg
	out := dc.Output
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return err
	}
	live, err := c.LiveSnapshot(ctx, dc)
	if err != nil {
		return fmt.Errorf("scaleway.TeardownOrphans live: %w", err)
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

	if rules := cfg.FirewallRules(); len(rules) > 0 {
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
	}

	// Include per-builder cache volumes in the desired set.
	_, _, builders := splitServers(cfg.ServerDefs())
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
	// Builder caches are always deleted on teardown — nvoi-owned, not user data.
	for _, b := range builders {
		collect(c.unmountAndDeleteVolume(ctx, dc, names, names.BuilderCacheVolumeShort(b.Name)))
	}

	for _, s := range builders {
		collect(c.drainAndDeleteServer(ctx, dc, names, s.Name))
	}
	for _, s := range workers {
		collect(c.drainAndDeleteServer(ctx, dc, names, s.Name))
	}
	for _, s := range masters {
		collect(c.drainAndDeleteServer(ctx, dc, names, s.Name))
	}

	c.sweepFirewalls(ctx, out, names.Base()+"-", nil)

	out.Command("network", "delete", names.Network())
	if err := c.DeleteNetwork(ctx, names.Network()); err != nil {
		collect(fmt.Errorf("network delete: %w", err))
	} else {
		out.Success(names.Network() + " deleted")
	}
	return firstErr
}

// ── orchestration helpers (mirror of hetzner / aws) ────────────────

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
			if errors.Is(err, infra.ErrHostKeyChanged) || errors.Is(err, infra.ErrAuthFailed) {
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

// ProvisionBuilders converges role: builder Scaleway instances + their
// per-builder cache BlockStorage volumes + the builder-only security group.
// Idempotent; runs BEFORE Bootstrap on the SSH-build dispatch path.
func (c *Client) ProvisionBuilders(ctx context.Context, dc *provider.BootstrapContext) error {
	_, _, builders := splitServers(dc.Cfg.ServerDefs())
	if len(builders) == 0 {
		return nil
	}
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return err
	}
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

// provisionBuilder is Scaleway's per-builder orchestration. Mirror of
// hetzner/aws shape: builder cloud-init (no k3s) + builder firewall →
// SSH wait → swap → cache volume + mount → enable docker + binfmt.
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
			if errors.Is(err, infra.ErrHostKeyChanged) || errors.Is(err, infra.ErrAuthFailed) {
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

	out.Progress("enabling docker on builder")
	if _, err := ssh.Run(ctx, "sudo systemctl enable --now docker.service docker.socket"); err != nil {
		return fmt.Errorf("enable docker: %w", err)
	}
	if err := ssh.RunStream(ctx, "sudo docker run --privileged --rm tonistiigi/binfmt:latest --install all", out.Writer(), out.Writer()); err != nil {
		out.Warning(fmt.Sprintf("binfmt register: %s", err))
	}
	out.Success(fmt.Sprintf("%s builder ready on %s", serverName, srv.IPv4))
	return nil
}

// BuilderTargets returns every role: builder Scaleway instance this provider
// manages for the current cluster. Lookup by label. Empty slice when none.
func (c *Client) BuilderTargets(ctx context.Context, dc *provider.BootstrapContext) ([]provider.BuilderTarget, error) {
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return nil, err
	}
	labels := names.Labels()
	labels["role"] = utils.RoleBuilder
	servers, err := c.ListServers(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("scaleway.BuilderTargets: %w", err)
	}
	targets := make([]provider.BuilderTarget, 0, len(servers))
	for _, s := range servers {
		targets = append(targets, provider.BuilderTarget{
			Name: s.Name,
			Host: s.IPv4,
			User: utils.DefaultUser,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets, nil
}

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

func (c *Client) drainAndDeleteServer(ctx context.Context, dc *provider.BootstrapContext, names *utils.Names, short string) error {
	out := dc.Output
	serverName := names.Server(short)

	if shell := c.cachedShell(); shell != nil {
		out.Command("node", "drain", serverName)
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

// dialSSH respects BootstrapContext.SSHDial when set (tests inject mock);
// production falls back to infra.ConnectSSH with the operator's pubkey.
func (c *Client) dialSSH(ctx context.Context, dc *provider.BootstrapContext, addr string) (utils.SSHClient, error) {
	if dc.SSHDial != nil {
		return dc.SSHDial(ctx, addr)
	}
	return infra.ConnectSSH(ctx, addr, utils.DefaultUser, dc.SSHKey)
}

// splitServers / serverNameSet / volumeNameSet / labelNodeViaSSH
// duplicated from hetzner + aws — see follow-up note in aws/infra.go
// about consolidating into pkg/provider/provisioning post-#47.
// splitServers: three-way split (masters / workers / builders). Builders
// are NOT iterated by Bootstrap — they are provisioned separately by
// ProvisionBuilders on the operator's machine. See CLAUDE.md.
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

func labelNodeViaSSH(ctx context.Context, ssh utils.SSHClient, nodeName, role string) error {
	cmd := fmt.Sprintf("sudo kubectl --kubeconfig=%s label node %s %s=%s --overwrite",
		utils.KubeconfigPath, nodeName, utils.LabelNvoiRole, role)
	_, err := ssh.Run(ctx, cmd)
	return err
}

// IngressBinding returns the master's public IPv4 wrapped in a DNS-A hint.
func (c *Client) IngressBinding(ctx context.Context, dc *provider.BootstrapContext, _ provider.ServiceTarget) (provider.IngressBinding, error) {
	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return provider.IngressBinding{}, err
	}
	return provider.IngressBinding{DNSType: "A", DNSTarget: master.IPv4}, nil
}

// HasPublicIngress: Scaleway instances have a routable public IPv4.
func (c *Client) HasPublicIngress() bool { return true }

// ConsumesBlocks: Scaleway reads the same IaaS YAML blocks Hetzner / AWS do.
func (c *Client) ConsumesBlocks() []string {
	return []string{"servers", "firewall", "volumes"}
}

// ValidateConfig: Scaleway-specific invariants. Today no-op — the generic
// validator covers what matters. Root disk size is configurable.
func (c *Client) ValidateConfig(cfg provider.ProviderConfigView) error {
	return nil
}

// NodeShell: cached connection from Bootstrap, or fresh dial via the
// public IPv4 looked up by label.
func (c *Client) NodeShell(ctx context.Context, dc *provider.BootstrapContext) (utils.SSHClient, error) {
	if s := c.cachedShell(); s != nil {
		return s, nil
	}
	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return nil, fmt.Errorf("scaleway.NodeShell: %w", err)
	}
	conn, err := c.dialSSH(ctx, dc, master.IPv4+":22")
	if err != nil {
		return nil, fmt.Errorf("scaleway.NodeShell dial %s: %w", master.IPv4, err)
	}
	c.setCachedShell(conn)
	return conn, nil
}

// Close releases the cached SSH if Bootstrap (or NodeShell's cold path)
// established one. Idempotent.
func (c *Client) Close() error {
	scalewayCacheMu.Lock()
	s := c.shell
	c.shell = nil
	scalewayCacheMu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}

// findMaster locates the master Scaleway instance by label. Returns
// (master, nil) on hit, (nil, provider.ErrNotBootstrapped) when absent,
// (nil, wrappedErr) on API failure. Callers distinguish via errors.Is.
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
		master.PrivateIP = ip
	}
	return master, nil
}

// Compile-time check that *Client satisfies provider.InfraProvider.
var _ provider.InfraProvider = (*Client)(nil)

// kube import retained for Bootstrap's Stage 2 fill-in.
var _ = (*kube.Client)(nil)
