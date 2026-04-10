package reconcile

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
)

// ValidateConfig catches all misconfigurations before touching infra.
// Returns the first error found — fail fast, fix one thing at a time.
func ValidateConfig(cfg *config.AppConfig) error {
	// ── Identity ──────────────────────────────────────────────────────────
	if cfg.App == "" {
		return fmt.Errorf("app is required")
	}
	if cfg.Env == "" {
		return fmt.Errorf("env is required")
	}

	// ── Providers ─────────────────────────────────────────────────────────
	if cfg.Providers.Compute == "" {
		return fmt.Errorf("providers.compute is required")
	}

	// ── Servers ───────────────────────────────────────────────────────────
	if len(cfg.Servers) == 0 {
		return fmt.Errorf("at least one server is required")
	}
	masterCount := 0
	for name, srv := range cfg.Servers {
		if srv.Type == "" {
			return fmt.Errorf("servers.%s.type is required", name)
		}
		if srv.Region == "" {
			return fmt.Errorf("servers.%s.region is required", name)
		}
		if srv.Role == "" {
			return fmt.Errorf("servers.%s.role is required (master or worker)", name)
		}
		if srv.Role != "master" && srv.Role != "worker" {
			return fmt.Errorf("servers.%s.role must be master or worker, got %q", name, srv.Role)
		}
		if srv.Role == "master" {
			masterCount++
		}
	}
	if masterCount == 0 {
		return fmt.Errorf("servers: exactly one server must have role: master")
	}
	if masterCount > 1 {
		return fmt.Errorf("servers: only one master is supported")
	}

	// ── Volumes ───────────────────────────────────────────────────────────
	for name, vol := range cfg.Volumes {
		if vol.Size <= 0 {
			return fmt.Errorf("volumes.%s.size must be > 0", name)
		}
		if vol.Server == "" {
			return fmt.Errorf("volumes.%s.server is required", name)
		}
		if _, ok := cfg.Servers[vol.Server]; !ok {
			return fmt.Errorf("volumes.%s.server: %q is not a defined server", name, vol.Server)
		}
	}

	// ── Build ─────────────────────────────────────────────────────────────
	for name, source := range cfg.Build {
		if source == "" {
			return fmt.Errorf("build.%s: source is required", name)
		}
	}

	// ── Services ──────────────────────────────────────────────────────────
	for name, svc := range cfg.Services {
		if svc.Image == "" && svc.Build == "" {
			return fmt.Errorf("services.%s: image or build is required", name)
		}
		if svc.Image != "" && svc.Build != "" {
			return fmt.Errorf("services.%s: image and build are mutually exclusive", name)
		}
		if svc.Build != "" {
			if _, ok := cfg.Build[svc.Build]; !ok {
				return fmt.Errorf("services.%s.build: %q is not a defined build target", name, svc.Build)
			}
		}
		if svc.Server != "" && len(svc.Servers) > 0 {
			return fmt.Errorf("services.%s: server and servers are mutually exclusive", name)
		}
		for _, s := range svc.ResolvedServers() {
			if _, ok := cfg.Servers[s]; !ok {
				return fmt.Errorf("services.%s.server: %q is not a defined server", name, s)
			}
		}
		if svc.Replicas < 0 {
			return fmt.Errorf("services.%s.replicas must be >= 0", name)
		}
		for _, ref := range svc.Storage {
			if _, ok := cfg.Storage[ref]; !ok {
				return fmt.Errorf("services.%s.storage: %q is not a defined storage", name, ref)
			}
		}
		if err := validateVolumeMounts(cfg, "services."+name, svc.Volumes); err != nil {
			return err
		}
		if len(svc.Servers) > 1 && hasNamedVolume(cfg, svc.Volumes) {
			return fmt.Errorf("services.%s: multiple servers with a volume mount — volumes are pinned to one server", name)
		}
		if err := validateVolumeServer(cfg, name, svc.Server, svc.Volumes); err != nil {
			return err
		}
	}

	// ── Crons ─────────────────────────────────────────────────────────────
	for name, cron := range cfg.Crons {
		if cron.Image == "" && cron.Build == "" {
			return fmt.Errorf("crons.%s: image or build is required", name)
		}
		if cron.Schedule == "" {
			return fmt.Errorf("crons.%s.schedule is required", name)
		}
		if cron.Build != "" {
			if _, ok := cfg.Build[cron.Build]; !ok {
				return fmt.Errorf("crons.%s.build: %q is not a defined build target", name, cron.Build)
			}
		}
		if cron.Server != "" && len(cron.Servers) > 0 {
			return fmt.Errorf("crons.%s: server and servers are mutually exclusive", name)
		}
		for _, s := range cron.ResolvedServers() {
			if _, ok := cfg.Servers[s]; !ok {
				return fmt.Errorf("crons.%s.server: %q is not a defined server", name, s)
			}
		}
		for _, ref := range cron.Storage {
			if _, ok := cfg.Storage[ref]; !ok {
				return fmt.Errorf("crons.%s.storage: %q is not a defined storage", name, ref)
			}
		}
		if err := validateVolumeMounts(cfg, "crons."+name, cron.Volumes); err != nil {
			return err
		}
		if len(cron.Servers) > 1 && hasNamedVolume(cfg, cron.Volumes) {
			return fmt.Errorf("crons.%s: multiple servers with a volume mount — volumes are pinned to one server", name)
		}
		if err := validateVolumeServer(cfg, name, cron.Server, cron.Volumes); err != nil {
			return err
		}
	}

	// ── Domains ───────────────────────────────────────────────────────────
	for svcName, domains := range cfg.Domains {
		svc, ok := cfg.Services[svcName]
		if !ok {
			return fmt.Errorf("domains.%s: %q is not a defined service", svcName, svcName)
		}
		if svc.Port == 0 {
			return fmt.Errorf("domains.%s: service %q has no port", svcName, svcName)
		}
		if len(domains) == 0 {
			return fmt.Errorf("domains.%s: at least one domain is required", svcName)
		}
		if svc.Replicas == 1 {
			return fmt.Errorf("services.%s: web-facing services require replicas >= 2 for zero-downtime rolling updates (omit replicas to default to 2)", svcName)
		}
	}

	return nil
}

func hasNamedVolume(cfg *config.AppConfig, mounts []string) bool {
	for _, mount := range mounts {
		source, _, ok := strings.Cut(mount, ":")
		if !ok {
			continue
		}
		if !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") {
			if _, ok := cfg.Volumes[source]; ok {
				return true
			}
		}
	}
	return false
}

func validateVolumeMounts(cfg *config.AppConfig, context string, mounts []string) error {
	for _, mount := range mounts {
		source, _, ok := strings.Cut(mount, ":")
		if !ok {
			return fmt.Errorf("%s.volumes: %q must be name:/path", context, mount)
		}
		if !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") {
			if _, ok := cfg.Volumes[source]; !ok {
				return fmt.Errorf("%s.volumes: volume %q is not defined", context, source)
			}
		}
	}
	return nil
}

func validateVolumeServer(cfg *config.AppConfig, workload, server string, mounts []string) error {
	for _, mount := range mounts {
		volName, _, ok := strings.Cut(mount, ":")
		if !ok || strings.HasPrefix(volName, "/") || strings.HasPrefix(volName, ".") {
			continue
		}
		vol, exists := cfg.Volumes[volName]
		if !exists {
			continue
		}
		if server != "" && server != vol.Server {
			return fmt.Errorf("%s: mounts volume %q (on server %q) but has server: %q — cannot move",
				workload, volName, vol.Server, server)
		}
	}
	return nil
}
