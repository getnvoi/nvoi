package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/kube"
	"github.com/getnvoi/nvoi/internal/provider"
)

type DNSSetRequest struct {
	Cluster
	DNSProvider string
	DNSCreds    map[string]string
	Service     string
	Domains     []string
}

func DNSSet(ctx context.Context, req DNSSetRequest) error {
	master, names, _, err := req.Cluster.Master(ctx)
	if err != nil {
		return err
	}

	dns, err := provider.ResolveDNS(req.DNSProvider, req.DNSCreds)
	if err != nil {
		return err
	}

	// DNS records always point at the master (Caddy runs there with hostNetwork)
	ip := master.IPv4

	fmt.Printf("==> dns set %s → %s\n", req.Service, ip)
	for _, domain := range req.Domains {
		fmt.Printf("  ensuring %s → %s...\n", domain, ip)
		if err := dns.EnsureARecord(ctx, domain, ip); err != nil {
			return fmt.Errorf("dns set %s: %w", domain, err)
		}
		fmt.Printf("  ✓ %s\n", domain)
	}

	// SSH to master for Caddy ingress
	ssh, err := connectToMaster(ctx, master.IPv4, req.SSHKey)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	// Read service port from the cluster
	port, err := kube.GetServicePort(ctx, ssh, ns, req.Service)
	if err != nil {
		return fmt.Errorf("service %q has no port — dns requires a service with --port: %w", req.Service, err)
	}

	// Read existing Caddy routes, merge with new route
	existing, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
	routes := mergeRoute(existing, kube.IngressRoute{
		Service: req.Service,
		Port:    port,
		Domains: req.Domains,
	})

	// Generate and apply Caddy manifest
	fmt.Printf("  applying caddy ingress...\n")
	yaml, err := kube.GenerateCaddyManifest(routes, names)
	if err != nil {
		return fmt.Errorf("generate caddy manifest: %w", err)
	}
	if err := kube.Apply(ctx, ssh, ns, yaml); err != nil {
		return fmt.Errorf("apply caddy: %w", err)
	}

	fmt.Printf("  waiting for caddy rollout...\n")
	if err := kube.WaitRollout(ctx, ssh, ns, names.KubeCaddy(), "deployment"); err != nil {
		return fmt.Errorf("caddy rollout: %w", err)
	}
	fmt.Printf("  ✓ caddy ready\n")

	// Poll until domain returns 200
	fmt.Printf("  waiting for https://%s...\n", req.Domains[0])
	httpClient := &http.Client{Timeout: 5 * time.Second}
	if err := core.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		resp, err := httpClient.Get("https://" + req.Domains[0])
		if err != nil {
			return false, nil
		}
		resp.Body.Close()
		return resp.StatusCode >= 200 && resp.StatusCode < 500, nil
	}); err != nil {
		fmt.Printf("  ⚠ domain not responding yet (TLS may still be provisioning)\n")
	} else {
		fmt.Printf("  ✓ https://%s live\n", req.Domains[0])
	}

	return nil
}

type DNSDeleteRequest struct {
	Cluster
	DNSProvider string
	DNSCreds    map[string]string
	Service     string
	Domains     []string
}

func DNSDelete(ctx context.Context, req DNSDeleteRequest) error {
	dns, err := provider.ResolveDNS(req.DNSProvider, req.DNSCreds)
	if err != nil {
		return err
	}

	fmt.Printf("==> dns delete %s\n", req.Service)
	for _, domain := range req.Domains {
		fmt.Printf("  deleting %s...\n", domain)
		if err := dns.DeleteARecord(ctx, domain); err != nil {
			return fmt.Errorf("dns delete %s: %w", domain, err)
		}
		fmt.Printf("  ✓ %s\n", domain)
	}

	// Remove route from Caddy if we have cluster access
	if req.Provider != "" {
		ssh, names, err := req.Cluster.SSH(ctx)
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
	DNSProvider string
	DNSCreds    map[string]string
}

func DNSList(ctx context.Context, req DNSListRequest) ([]provider.DNSRecord, error) {
	dns, err := provider.ResolveDNS(req.DNSProvider, req.DNSCreds)
	if err != nil {
		return nil, err
	}
	return dns.ListARecords(ctx)
}

// connectToMaster is a shorthand for SSH connect to a known master IP.
func connectToMaster(ctx context.Context, ip string, sshKey []byte) (core.SSHClient, error) {
	ssh, err := infra.ConnectSSH(ctx, ip+":22", core.DefaultUser, sshKey)
	if err != nil {
		return nil, fmt.Errorf("ssh master: %w", err)
	}
	return ssh, nil
}

// mergeRoute adds or replaces a route in the list by service name.
func mergeRoute(routes []kube.IngressRoute, newRoute kube.IngressRoute) []kube.IngressRoute {
	for i, r := range routes {
		if r.Service == newRoute.Service {
			routes[i] = newRoute
			return routes
		}
	}
	return append(routes, newRoute)
}

// removeRoute removes a route by service name.
func removeRoute(routes []kube.IngressRoute, service string) []kube.IngressRoute {
	var result []kube.IngressRoute
	for _, r := range routes {
		if r.Service != service {
			result = append(result, r)
		}
	}
	return result
}
