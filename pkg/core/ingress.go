package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── Types ────────────────────────────────────────────────────────────────────

// IngressRouteArg is a parsed service:domain,domain arg.
type IngressRouteArg struct {
	Service string
	Domains []string
}

// IngressHooks holds injectable dependencies for testing.
// Nil fields use production defaults.
type IngressHooks struct {
	WaitForCertificate func(ctx context.Context, ssh utils.SSHClient, certPath string) error
	WaitForHTTPS       func(ctx context.Context, ssh utils.SSHClient, domain string) error
}

func (h *IngressHooks) waitForCertificate() func(context.Context, utils.SSHClient, string) error {
	if h != nil && h.WaitForCertificate != nil {
		return h.WaitForCertificate
	}
	return infra.WaitForCertificate
}

func (h *IngressHooks) waitForHTTPS() func(context.Context, utils.SSHClient, string) error {
	if h != nil && h.WaitForHTTPS != nil {
		return h.WaitForHTTPS
	}
	return infra.WaitForHTTPS
}

type IngressSetRequest struct {
	Cluster
	Route IngressRouteArg
	Hooks *IngressHooks // nil = production defaults
}

type IngressDeleteRequest struct {
	Cluster
	Route IngressRouteArg // route to remove
}

// ── ParseIngressArgs ────────────────────────────────────────────────────────

// ParseIngressArgs parses "service:domain,domain" args.
func ParseIngressArgs(args []string) ([]IngressRouteArg, error) {
	var routes []IngressRouteArg
	for _, arg := range args {
		service, domainPart, ok := strings.Cut(arg, ":")
		if !ok || service == "" || domainPart == "" {
			return nil, fmt.Errorf("invalid route %q — expected service:domain,domain", arg)
		}
		var domains []string
		for _, d := range strings.Split(domainPart, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				domains = append(domains, d)
			}
		}
		if len(domains) == 0 {
			return nil, fmt.Errorf("invalid route %q — no domains", arg)
		}
		routes = append(routes, IngressRouteArg{Service: service, Domains: domains})
	}
	return routes, nil
}

// ── IngressSet ──────────────────────────────────────────────────────────────

// IngressSet adds or updates a single ingress route. Reads the current Caddyfile,
// merges the new route, and redeploys Caddy with ACME TLS.
func IngressSet(ctx context.Context, req IngressSetRequest) error {
	out := req.Log()

	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	out.Command("ingress", "set", req.Route.Service, "domains", req.Route.Domains)
	waitForCert := req.Hooks.waitForCertificate()
	waitForHTTPS := req.Hooks.waitForHTTPS()

	// Resolve the service port from the cluster.
	port, err := kube.GetServicePort(ctx, ssh, ns, req.Route.Service)
	if err != nil {
		return fmt.Errorf("service %q has no port — ingress requires a service with --port: %w", req.Route.Service, err)
	}

	// Read current routes and merge.
	currentRoutes, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
	merged := mergeRoute(currentRoutes, kube.IngressRoute{
		Service: req.Route.Service,
		Port:    port,
		Domains: req.Route.Domains,
	})

	out.Progress("applying caddy config")
	if err := kube.ApplyCaddyConfig(ctx, ssh, ns, merged, names); err != nil {
		return fmt.Errorf("caddy: %w", err)
	}
	out.Success("caddy ready")

	// Verify cert + HTTPS from the server (no dependency on client DNS).
	firstDomain := req.Route.Domains[0]
	certPath := names.CaddyCertPath(firstDomain)
	out.Progress(fmt.Sprintf("waiting for certificate %s", firstDomain))
	if err := waitForCert(ctx, ssh, certPath); err != nil {
		return fmt.Errorf("certificate for %s not provisioned: %w", firstDomain, err)
	}
	out.Success(fmt.Sprintf("certificate %s ready", firstDomain))

	out.Progress(fmt.Sprintf("waiting for https://%s", firstDomain))
	if err := waitForHTTPS(ctx, ssh, firstDomain); err != nil {
		return fmt.Errorf("https://%s not reachable: %w", firstDomain, err)
	}
	out.Success(fmt.Sprintf("https://%s live", firstDomain))

	return nil
}

// ── IngressDelete ───────────────────────────────────────────────────────────

// IngressDelete removes a single ingress route.
//
// When routes remain after removal: remove the route from Caddyfile, redeploy Caddy.
// When no routes remain (last route deleted): wipe deployment, configmap.
func IngressDelete(ctx context.Context, req IngressDeleteRequest) error {
	out := req.Log()
	out.Command("ingress", "delete", req.Route.Service, "domains", req.Route.Domains)

	ssh, names, sshErr := req.Cluster.SSH(ctx)
	if sshErr != nil {
		if errors.Is(sshErr, ErrNoMaster) {
			out.Success("cluster gone — local resources already absent")
			return nil
		}
		return sshErr
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	// Read current routes, remove the target service.
	currentRoutes, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
	remaining := removeRoute(currentRoutes, req.Route.Service)

	if len(remaining) > 0 {
		// Routes remain — just remove the route, redeploy.
		out.Progress(fmt.Sprintf("removing route, %d remaining", len(remaining)))
		if err := kube.ApplyCaddyConfig(ctx, ssh, ns, remaining, names); err != nil {
			return fmt.Errorf("caddy: %w", err)
		}
		out.Success(fmt.Sprintf("caddy updated (%d route(s) remaining)", len(remaining)))
		return nil
	}

	out.Progress("removing caddy ingress")
	if err := deleteAllIngress(ctx, ssh, ns, names); err != nil {
		return err
	}
	out.Success("ingress removed")
	return nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// mergeRoute replaces or appends a route in the current set, matched by service name.
func mergeRoute(current []kube.IngressRoute, route kube.IngressRoute) []kube.IngressRoute {
	merged := make([]kube.IngressRoute, 0, len(current)+1)
	replaced := false
	for _, r := range current {
		if r.Service == route.Service {
			merged = append(merged, route)
			replaced = true
		} else {
			merged = append(merged, r)
		}
	}
	if !replaced {
		merged = append(merged, route)
	}
	return merged
}

// removeRoute removes the route for the given service name.
func removeRoute(current []kube.IngressRoute, service string) []kube.IngressRoute {
	var remaining []kube.IngressRoute
	for _, r := range current {
		if r.Service != service {
			remaining = append(remaining, r)
		}
	}
	return remaining
}

func deleteAllIngress(ctx context.Context, ssh utils.SSHClient, ns string, names *utils.Names) error {
	if err := kube.DeleteByName(ctx, ssh, ns, names.KubeCaddy()); err != nil {
		return err
	}
	if _, err := kube.RunKubectl(ctx, ssh, ns, fmt.Sprintf("delete configmap %s --ignore-not-found", names.KubeCaddyConfig())); err != nil {
		return err
	}
	return nil
}
