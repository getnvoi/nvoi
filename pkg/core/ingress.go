package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// acmeVerifyTimeout bounds the total time spent verifying certs + HTTPS
// across all domains for a single service. If it expires, the deploy
// continues with a warning — certs finish issuing in the background.
var acmeVerifyTimeout = 10 * time.Minute

// ── Types ────────────────────────────────────────────────────────────────────

// IngressRouteArg is a parsed service:domain,domain arg.
type IngressRouteArg struct {
	Service string
	Domains []string
}

// IngressHooks holds injectable dependencies for testing.
type IngressHooks struct {
	WaitForCertificate func(ctx context.Context, ssh utils.SSHClient, domain string) error
	WaitForHTTPS       func(ctx context.Context, ssh utils.SSHClient, domain, healthPath string) error
}

func (h *IngressHooks) waitForCertificate() func(context.Context, utils.SSHClient, string) error {
	if h != nil && h.WaitForCertificate != nil {
		return h.WaitForCertificate
	}
	return infra.WaitForCertificate
}

func (h *IngressHooks) waitForHTTPS() func(context.Context, utils.SSHClient, string, string) error {
	if h != nil && h.WaitForHTTPS != nil {
		return h.WaitForHTTPS
	}
	return infra.WaitForHTTPS
}

type IngressSetRequest struct {
	Cluster
	Route      IngressRouteArg
	HealthPath string        // if set, verify HTTPS responds on this path after cert
	ACME       bool          // true = Traefik ACME (Let's Encrypt), false = HTTP only (tunnel)
	Hooks      *IngressHooks // nil = production defaults
}

type IngressDeleteRequest struct {
	Cluster
	Route IngressRouteArg
}

// ── ParseIngressArgs ────────────────────────────────────────────────────────

// ParseIngressArgs parses "service:domain,domain" args.
func ParseIngressArgs(args []string) ([]IngressRouteArg, error) {
	var routes []IngressRouteArg
	for _, arg := range args {
		service, domainPart, ok := strings.Cut(arg, ":")
		if !ok || service == "" || domainPart == "" {
			return nil, ErrInputf("invalid route %q — expected service:domain,domain", arg)
		}
		var domains []string
		for _, d := range strings.Split(domainPart, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				domains = append(domains, d)
			}
		}
		if len(domains) == 0 {
			return nil, ErrInputf("invalid route %q — no domains", arg)
		}
		routes = append(routes, IngressRouteArg{Service: service, Domains: domains})
	}
	return routes, nil
}

// ── IngressSet ──────────────────────────────────────────────────────────────

// IngressSet creates or updates a k8s Ingress resource for a service.
// One Ingress per service — no shared state, no read-modify-write.
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

	// Resolve the service port from the cluster.
	port, err := kube.GetServicePort(ctx, ssh, ns, req.Route.Service)
	if err != nil {
		return fmt.Errorf("service %q has no port — ingress requires a service with --port: %w", req.Route.Service, err)
	}

	out.Progress("applying ingress")
	if err := kube.ApplyIngress(ctx, ssh, ns, kube.IngressRoute{
		Service: req.Route.Service,
		Port:    port,
		Domains: req.Route.Domains,
	}, req.ACME); err != nil {
		return fmt.Errorf("ingress: %w", err)
	}
	out.Success("ingress ready")

	if req.ACME {
		waitForCert := req.Hooks.waitForCertificate()
		waitForHTTPS := req.Hooks.waitForHTTPS()
		healthPath := req.HealthPath
		if healthPath == "" {
			healthPath = "/"
		}

		// Single deadline across all domains. If it expires, warn and
		// continue — certs finish issuing in the background, next deploy
		// will confirm them.
		acmeCtx, cancel := context.WithTimeout(ctx, acmeVerifyTimeout)
		defer cancel()

		for _, domain := range req.Route.Domains {
			// Step 1: cert issued by ACME
			out.Progress(fmt.Sprintf("waiting for certificate %s", domain))
			if err := waitForCert(acmeCtx, ssh, domain); err != nil {
				if acmeCtx.Err() != nil {
					out.Warning(fmt.Sprintf("ACME verification timed out at %s — certs may still be issuing. Next deploy will re-verify.", domain))
					return nil
				}
				return fmt.Errorf("certificate for %s not provisioned: %w", domain, err)
			}
			out.Success(fmt.Sprintf("certificate %s ready", domain))

			// Step 2: service reachable over HTTPS
			url := fmt.Sprintf("https://%s%s", domain, healthPath)
			out.Progress(fmt.Sprintf("waiting for %s", url))
			if err := waitForHTTPS(acmeCtx, ssh, domain, healthPath); err != nil {
				if acmeCtx.Err() != nil {
					out.Warning(fmt.Sprintf("ACME verification timed out at %s — certs may still be issuing. Next deploy will re-verify.", domain))
					return nil
				}
				return fmt.Errorf("%s not reachable: %w", url, err)
			}
			out.Success(fmt.Sprintf("%s live", url))
		}
	}

	return nil
}

// ── IngressDelete ───────────────────────────────────────────────────────────

// IngressDelete removes the Ingress resource for a service.
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
	out.Progress("removing ingress")
	if err := kube.DeleteIngress(ctx, ssh, ns, req.Route.Service); err != nil {
		return err
	}
	out.Success("ingress removed")
	return nil
}
