package config

import (
	"fmt"
	"strings"
)

const (
	ingressExposureDirect      = "direct"
	ingressExposureEdgeProxied = "edge_proxied"

	ingressTLSACME        = "acme"
	ingressTLSProvided    = "provided"
	ingressTLSEdgeOrigin  = "edge_origin"
	ingressEdgeCloudflare = "cloudflare"
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
	// Empty services is valid — used for destroy-via-diff.
	// Collect managed service names for database ref validation.
	managedServices := map[string]bool{}
	for name, svc := range cfg.Services {
		if svc.Managed != "" {
			managedServices[name] = true
		}
	}

	for name, svc := range cfg.Services {
		// Exactly one of image, build, managed.
		sources := 0
		if svc.Image != "" {
			sources++
		}
		if svc.Build != "" {
			sources++
		}
		if svc.Managed != "" {
			sources++
		}
		if sources == 0 {
			add("services.%s: must have one of image, build, or managed", name)
		}
		if sources > 1 {
			add("services.%s: image, build, and managed are mutually exclusive", name)
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
		for _, ref := range svc.Uses {
			if !managedServices[ref] {
				add("services.%s.uses: %q is not a managed service", name, ref)
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

	// ── Orphan volumes ─────────────────────────────────────────────────────────
	// A volume that no service mounts is likely a mistake.
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
		for name := range cfg.Volumes {
			if !referencedVolumes[name] {
				add("volumes.%s: defined but not mounted by any service", name)
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

	// ── Firewall × Edge overlay coherence ─────────────────────────────────────
	isCloudflareFirewall := cfg.Firewall != nil && cfg.Firewall.Preset == "cloudflare"
	exposure := desiredIngressExposure(cfg)
	tlsMode := desiredIngressTLSMode(cfg, exposure)
	edgeProvider := desiredIngressEdgeProvider(cfg)

	if exposure == ingressExposureEdgeProxied && !isCloudflareFirewall {
		add("ingress.exposure: proxied edge mode currently requires \"firewall: cloudflare\" — origin is directly reachable without it")
	}
	if isCloudflareFirewall && exposure != ingressExposureEdgeProxied {
		add("firewall: preset \"cloudflare\" requires ingress.exposure: edge_proxied")
	}

	// ── Ingress TLS / edge overlay coherence ──────────────────────────────────
	switch exposure {
	case "", ingressExposureDirect, ingressExposureEdgeProxied:
	default:
		add("ingress.exposure: must be one of %q or %q", ingressExposureDirect, ingressExposureEdgeProxied)
	}

	if cfg.Ingress != nil && cfg.Ingress.Edge != nil && edgeProvider != "" && edgeProvider != ingressEdgeCloudflare {
		add("ingress.edge.provider: unsupported provider %q", edgeProvider)
	}
	if edgeProvider != "" && exposure != ingressExposureEdgeProxied {
		add("ingress.edge.provider: edge overlays require ingress.exposure: edge_proxied")
	}

	switch tlsMode {
	case ingressTLSACME, ingressTLSProvided, ingressTLSEdgeOrigin:
	default:
		add("ingress.tls.mode: must be one of %q, %q, or %q", ingressTLSACME, ingressTLSProvided, ingressTLSEdgeOrigin)
	}

	if cfg.Ingress != nil && cfg.Ingress.TLS != nil {
		hasCert := cfg.Ingress.TLS.Cert != ""
		hasKey := cfg.Ingress.TLS.Key != ""
		if hasCert != hasKey {
			add("ingress.tls: cert and key must both be set")
		}
		if tlsMode == ingressTLSProvided && (!hasCert || !hasKey) {
			add("ingress.tls: mode \"provided\" requires both cert and key env refs")
		}
		if tlsMode != ingressTLSProvided && (hasCert || hasKey) {
			add("ingress.tls: cert/key refs are only valid with mode \"provided\"")
		}
	}

	if tlsMode == ingressTLSEdgeOrigin {
		if exposure != ingressExposureEdgeProxied {
			add("ingress.tls.mode: %q requires ingress.exposure: %s", ingressTLSEdgeOrigin, ingressExposureEdgeProxied)
		}
		if edgeProvider != ingressEdgeCloudflare {
			add("ingress.tls.mode: %q currently requires ingress.edge.provider: %q", ingressTLSEdgeOrigin, ingressEdgeCloudflare)
		}
	}

	return errs
}

// firewallOpensPort checks if a FirewallConfig opens the given port.
// A preset that includes the port counts (default includes 80/443, cloudflare includes 80/443).
func firewallOpensPort(fw *FirewallConfig, port string) bool {
	// Presets that include HTTP ports
	if fw.Preset == "default" || fw.Preset == "cloudflare" {
		if port == "80" || port == "443" {
			return true
		}
	}
	// Explicit rules
	if cidrs, ok := fw.Rules[port]; ok && len(cidrs) > 0 {
		return true
	}
	return false
}

func desiredIngressExposure(cfg *Config) string {
	if cfg.Ingress != nil && cfg.Ingress.Exposure != "" {
		return cfg.Ingress.Exposure
	}
	return ingressExposureDirect
}

func desiredIngressTLSMode(cfg *Config, exposure string) string {
	if cfg.Ingress != nil && cfg.Ingress.TLS != nil && cfg.Ingress.TLS.Mode != "" {
		return cfg.Ingress.TLS.Mode
	}
	if exposure == ingressExposureEdgeProxied {
		return ingressTLSEdgeOrigin
	}
	return ingressTLSACME
}

func desiredIngressEdgeProvider(cfg *Config) string {
	if cfg.Ingress != nil && cfg.Ingress.Edge != nil {
		return cfg.Ingress.Edge.Provider
	}
	return ""
}
