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
	if len(cfg.Servers) == 0 {
		add("servers: at least one server is required")
	}
	for name, srv := range cfg.Servers {
		if srv.Type == "" {
			add("servers.%s.type: required", name)
		}
		if srv.Region == "" {
			add("servers.%s.region: required", name)
		}
	}

	// ── Volumes ────────────────────────────────────────────────────────────────
	for name, vol := range cfg.Volumes {
		if vol.Size <= 0 {
			add("volumes.%s.size: must be > 0", name)
		}
		if vol.Server == "" {
			add("volumes.%s.server: required", name)
		} else if _, ok := cfg.Servers[vol.Server]; !ok {
			add("volumes.%s.server: %q is not a defined server", name, vol.Server)
		}
	}

	// ── Build ──────────────────────────────────────────────────────────────────
	for name, b := range cfg.Build {
		if b.Source == "" {
			add("build.%s.source: required", name)
		}
	}

	// ── Services ───────────────────────────────────────────────────────────────
	if len(cfg.Services) == 0 {
		add("services: at least one service is required")
	}
	for name, svc := range cfg.Services {
		if svc.Image == "" && svc.Build == "" {
			add("services.%s: must have either image or build", name)
		}
		if svc.Image != "" && svc.Build != "" {
			add("services.%s: cannot have both image and build", name)
		}
		if svc.Build != "" {
			if _, ok := cfg.Build[svc.Build]; !ok {
				add("services.%s.build: %q is not a defined build target", name, svc.Build)
			}
		}
		if svc.Server != "" {
			if _, ok := cfg.Servers[svc.Server]; !ok {
				add("services.%s.server: %q is not a defined server", name, svc.Server)
			}
		}
		if svc.Replicas < 0 {
			add("services.%s.replicas: must be >= 0", name)
		}
		for _, ref := range svc.Storage {
			if _, ok := cfg.Storage[ref]; !ok {
				add("services.%s.storage: %q is not a defined storage", name, ref)
			}
		}
		for _, mount := range svc.Volumes {
			source, _, ok := strings.Cut(mount, ":")
			if !ok {
				add("services.%s.volumes: %q must be name:/path", name, mount)
				continue
			}
			// Named volumes must be defined.
			if !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") {
				if _, ok := cfg.Volumes[source]; !ok {
					add("services.%s.volumes: volume %q is not defined", name, source)
				}
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

	return errs
}
