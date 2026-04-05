package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

type DNSSetRequest struct {
	Cluster
	DNS     ProviderRef
	Service string
	Domains []string
}

func DNSSet(ctx context.Context, req DNSSetRequest) error {
	out := req.Log()

	master, names, _, err := req.Cluster.Master(ctx)
	if err != nil {
		return err
	}

	dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
	if err != nil {
		return err
	}

	ip := master.IPv4
	out.Command("dns", "set", req.Service, "ip", ip, "domains", req.Domains)

	for _, domain := range req.Domains {
		out.Progress(fmt.Sprintf("ensuring %s → %s", domain, ip))
		if err := dns.EnsureARecord(ctx, domain, ip); err != nil {
			return fmt.Errorf("dns set %s: %w", domain, err)
		}
		out.Success(domain)
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", utils.DefaultUser, req.SSHKey)
	if err != nil {
		return fmt.Errorf("ssh master: %w", err)
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	port, err := kube.GetServicePort(ctx, ssh, ns, req.Service)
	if err != nil {
		return fmt.Errorf("service %q has no port — dns requires a service with --port: %w", req.Service, err)
	}

	existing, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
	routes := mergeRoute(existing, kube.IngressRoute{
		Service: req.Service,
		Port:    port,
		Domains: req.Domains,
	})

	out.Progress("applying caddy ingress")
	yaml, err := kube.GenerateCaddyManifest(routes, names)
	if err != nil {
		return fmt.Errorf("generate caddy manifest: %w", err)
	}
	if err := kube.Apply(ctx, ssh, ns, yaml); err != nil {
		return fmt.Errorf("apply caddy: %w", err)
	}

	out.Progress("waiting for caddy rollout")
	if err := kube.WaitRollout(ctx, ssh, ns, names.KubeCaddy(), "deployment", out); err != nil {
		return fmt.Errorf("caddy rollout: %w", err)
	}
	out.Success("caddy ready")

	out.Progress(fmt.Sprintf("waiting for https://%s", req.Domains[0]))
	if err := infra.WaitHTTPS(ctx, req.Domains[0]); err != nil {
		out.Warning("domain not responding yet (TLS may still be provisioning)")
	} else {
		out.Success(fmt.Sprintf("https://%s live", req.Domains[0]))
	}

	return nil
}

type DNSDeleteRequest struct {
	Cluster
	DNS     ProviderRef
	Service string
	Domains []string
}

func DNSDelete(ctx context.Context, req DNSDeleteRequest) error {
	out := req.Log()
	out.Command("dns", "delete", req.Service)

	dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
	if err != nil {
		return err
	}

	for _, domain := range req.Domains {
		out.Progress(fmt.Sprintf("deleting %s", domain))
		if err := dns.DeleteARecord(ctx, domain); err != nil {
			return fmt.Errorf("dns delete %s: %w", domain, err)
		}
	}

	// Remove route from Caddy if we have cluster access
	if req.Provider != "" {
		ssh, names, err := req.Cluster.SSH(ctx)
		if errors.Is(err, ErrNoMaster) {
			return nil
		}
		if err != nil {
			return nil
		}
		defer ssh.Close()

		ns := names.KubeNamespace()
		existing, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
		routes := removeRoute(existing, req.Service)

		if len(routes) == 0 {
			kube.DeleteByName(ctx, ssh, ns, names.KubeCaddy())
			kube.RunKubectl(ctx, ssh, ns, fmt.Sprintf("delete configmap %s --ignore-not-found", names.KubeCaddyConfig()))
		} else {
			yaml, err := kube.GenerateCaddyManifest(routes, names)
			if err == nil {
				kube.Apply(ctx, ssh, ns, yaml)
			}
		}
	}

	return nil
}

type DNSListRequest struct {
	DNS    ProviderRef
	Output Output
}

func DNSList(ctx context.Context, req DNSListRequest) ([]provider.DNSRecord, error) {
	dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
	if err != nil {
		return nil, err
	}
	return dns.ListARecords(ctx)
}

func mergeRoute(routes []kube.IngressRoute, newRoute kube.IngressRoute) []kube.IngressRoute {
	for i, r := range routes {
		if r.Service == newRoute.Service {
			routes[i] = newRoute
			return routes
		}
	}
	return append(routes, newRoute)
}

func removeRoute(routes []kube.IngressRoute, service string) []kube.IngressRoute {
	var result []kube.IngressRoute
	for _, r := range routes {
		if r.Service != service {
			result = append(result, r)
		}
	}
	return result
}
