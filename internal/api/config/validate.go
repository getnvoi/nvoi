package config

import (
	"fmt"
	"strings"
)

// Validate checks the config for structural errors.
// Returns all errors found, not just the first.
func Validate(cfg *Config) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	// ── Servers ────────────────────────────────────────────────────────────────
	// Empty config is valid — used for destroy-via-diff.
	masterCount := 0
	for name, srv := range cfg.Servers {
		if srv.Type == "" {
			add("servers.%s.type: required", name)
		}
		if srv.Region == "" {
			add("servers.%s.region: required", name)
		}
		if srv.Role == "" {
			add("servers.%s.role: required (master or worker)", name)
		} else if srv.Role != "master" && srv.Role != "worker" {
			add("servers.%s.role: must be master or worker, got %q", name, srv.Role)
		}
		if srv.Role == "master" {
			masterCount++
		}
	}
	if len(cfg.Servers) > 0 && masterCount == 0 {
		add("servers: exactly one server must have role: master")
	}
	if masterCount > 1 {
		add("servers: multiple masters not yet supported (HA requires k3s server --server join, not implemented)")
	}

	// ── Volumes ────────────────────────────────────────────────────────────────
	for name, vol := range cfg.Volumes {
		if vol.Size <= 0 {
			add("volumes.%s.size: must be > 0", name)
		}
		if vol.Server == "" {
			add("volumes.%s.server: required", name)
		} else if _, ok := cfg.Servers[vol.Server]; !ok {
			add("volumes.%s.server: %q is not a defined server — run 'nvoi instance set %s' first", name, vol.Server, vol.Server)
		}
	}

	// ── Build ──────────────────────────────────────────────────────────────────
	for name, b := range cfg.Build {
		if b.Source == "" {
			add("build.%s.source: required", name)
		}
	}

	// ── Services ───────────────────────────────────────────────────────────────
	managedServices := map[string]bool{}
	for name, svc := range cfg.Services {
		if svc.Managed != "" {
			managedServices[name] = true
		}
	}

	for name, svc := range cfg.Services {
		if svc.Managed == "" && svc.Image == "" {
			add("services.%s: image is required (or managed for managed services)", name)
		}
		if svc.Managed != "" {
			if svc.Port > 0 {
				add("services.%s: port not supported on managed services", name)
			}
			if svc.Replicas > 0 {
				add("services.%s: replicas not supported on managed services", name)
			}
			if svc.Command != "" {
				add("services.%s: command not supported on managed services", name)
			}
			if svc.Health != "" {
				add("services.%s: health not supported on managed services", name)
			}
			if len(svc.Env) > 0 {
				add("services.%s: env not supported on managed services (credentials are injected automatically)", name)
			}
			if len(svc.Uses) > 0 {
				add("services.%s: uses not supported on managed services", name)
			}
		}
		if svc.Server != "" {
			if _, ok := cfg.Servers[svc.Server]; !ok {
				add("services.%s.server: %q is not a defined server — run 'nvoi instance set %s' first", name, svc.Server, svc.Server)
			}
		}
		if svc.Replicas < 0 {
			add("services.%s.replicas: must be >= 0", name)
		}
		for _, ref := range svc.Storage {
			if _, ok := cfg.Storage[ref]; !ok {
				add("services.%s.storage: %q is not a defined storage — run 'nvoi storage set %s' first", name, ref, ref)
			}
		}
		for _, ref := range svc.Uses {
			if !managedServices[ref] {
				add("services.%s.uses: %q is not a managed service — run 'nvoi database set %s' or 'nvoi agent set %s' first", name, ref, ref, ref)
			}
		}
		for _, mount := range svc.Volumes {
			source, _, ok := strings.Cut(mount, ":")
			if !ok {
				add("services.%s.volumes: %q must be name:/path", name, mount)
				continue
			}
			if !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") {
				if _, ok := cfg.Volumes[source]; !ok {
					add("services.%s.volumes: volume %q is not defined — run 'nvoi volume set %s' first", name, source, source)
				}
			}
		}
	}

	// ── Crons ─────────────────────────────────────────────────────────────────
	for name, cron := range cfg.Crons {
		if cron.Image == "" {
			add("crons.%s.image: required", name)
		}
		if cron.Schedule == "" {
			add("crons.%s.schedule: required", name)
		}
		if cron.Server != "" {
			if _, ok := cfg.Servers[cron.Server]; !ok {
				add("crons.%s.server: %q is not a defined server — run 'nvoi instance set %s' first", name, cron.Server, cron.Server)
			}
		}
		for _, ref := range cron.Storage {
			if _, ok := cfg.Storage[ref]; !ok {
				add("crons.%s.storage: %q is not a defined storage — run 'nvoi storage set %s' first", name, ref, ref)
			}
		}
		for _, mount := range cron.Volumes {
			source, _, ok := strings.Cut(mount, ":")
			if !ok {
				add("crons.%s.volumes: %q must be name:/path", name, mount)
				continue
			}
			if !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") {
				if _, ok := cfg.Volumes[source]; !ok {
					add("crons.%s.volumes: volume %q is not defined — run 'nvoi volume set %s' first", name, source, source)
				}
			}
		}
	}

	// ── Orphan volumes ─────────────────────────────────────────────────────────
	if len(cfg.Volumes) > 0 {
		referencedVolumes := map[string]bool{}
		for _, svc := range cfg.Services {
			for _, mount := range svc.Volumes {
				source, _, ok := strings.Cut(mount, ":")
				if ok {
					referencedVolumes[source] = true
				}
			}
		}
		for _, cron := range cfg.Crons {
			for _, mount := range cron.Volumes {
				source, _, ok := strings.Cut(mount, ":")
				if ok {
					referencedVolumes[source] = true
				}
			}
		}
		for name := range cfg.Volumes {
			if !referencedVolumes[name] {
				add("volumes.%s: defined but not mounted by any service or cron", name)
			}
		}
	}

	// ── Domains ────────────────────────────────────────────────────────────────
	for svcName, domains := range cfg.Domains {
		svc, ok := cfg.Services[svcName]
		if !ok {
			add("domains.%s: %q is not a defined service", svcName, svcName)
			continue
		}
		if svc.Port == 0 {
			add("domains.%s: service %q has no port", svcName, svcName)
		}
		if len(domains) == 0 {
			add("domains.%s: at least one domain is required", svcName)
		}
	}

	// ── Firewall × Domains coherence ──────────────────────────────────────────
	if len(cfg.Domains) > 0 && cfg.Firewall == nil {
		add("firewall: domains configured but no firewall section — add \"firewall: default\" or explicit 80/443 rules")
	}
	if len(cfg.Domains) > 0 && cfg.Firewall != nil {
		has80, has443 := firewallOpensPort(cfg.Firewall, "80"), firewallOpensPort(cfg.Firewall, "443")
		if !has80 || !has443 {
			add("firewall: domains configured but ports 80/443 not open — add to firewall section or use \"firewall: default\"")
		}
	}

	// ── Ingress coherence ─────────────────────────────────────────────────────
	// Only check firewall↔ingress coherence when domains are configured.
	// During incremental config build-up, firewall may be set before ingress.
	isCloudflareFirewall := cfg.Firewall != nil && cfg.Firewall.Preset == "cloudflare"
	isCloudflareManaged := cfg.Ingress != nil && cfg.Ingress.CloudflareManaged
	hasCustomCert := cfg.Ingress != nil && cfg.Ingress.Cert != ""
	hasDomains := len(cfg.Domains) > 0

	if hasDomains {
		if isCloudflareFirewall && !isCloudflareManaged {
			add("firewall: preset \"cloudflare\" requires ingress.cloudflare-managed: true")
		}
		if isCloudflareManaged && !isCloudflareFirewall {
			add("ingress: cloudflare-managed requires \"firewall: cloudflare\"")
		}
	}
	if isCloudflareManaged && hasCustomCert {
		add("ingress: cloudflare-managed and cert/key are mutually exclusive")
	}
	if hasCustomCert {
		if cfg.Ingress.Key == "" {
			add("ingress: cert requires key")
		}
	}
	if cfg.Ingress != nil && cfg.Ingress.Key != "" && cfg.Ingress.Cert == "" {
		add("ingress: key requires cert")
	}

	return errs
}

// firewallOpensPort checks if a FirewallConfig opens the given port.
func firewallOpensPort(fw *FirewallConfig, port string) bool {
	if fw.Preset == "default" || fw.Preset == "cloudflare" {
		if port == "80" || port == "443" {
			return true
		}
	}
	if cidrs, ok := fw.Rules[port]; ok && len(cidrs) > 0 {
		return true
	}
	return false
}
