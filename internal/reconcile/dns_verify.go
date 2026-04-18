package reconcile

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// dnsGracePeriod is the wait before checking DNS propagation. Variable for tests.
var dnsGracePeriod = 3 * time.Second

// verifyDNSPropagation checks that domains resolve to the expected server IP.
// Runs the lookup from the master server via SSH — same resolver Caddy will
// use when it runs the ACME HTTP-01 challenge. If DNS hasn't propagated from
// the server's perspective, warns the user but does NOT block the deploy —
// Caddy keeps retrying ACME for the full caddyCertTimeout. This just gives
// the user an early heads-up so they don't blame nvoi for the slow path.
func verifyDNSPropagation(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) {
	out := dc.Cluster.Log()

	ssh := dc.Cluster.NodeShell
	if ssh == nil {
		return
	}

	// Get the public endpoint via InfraProvider.IngressBinding — same
	// source the DNS step routes domains to. For IaaS providers this is
	// the master IPv4; for managed-k8s it'd be a load-balancer hostname
	// (we'd skip the resolve check then since DNSType wouldn't be "A").
	bctx := config.BootstrapContext(dc, cfg)
	infra, err := provider.ResolveInfra(bctx.ProviderName, dc.Cluster.Credentials)
	if err != nil {
		return
	}
	defer func() { _ = infra.Close() }()

	binding, err := infra.IngressBinding(ctx, bctx, provider.ServiceTarget{})
	if err != nil || binding.DNSType != "A" {
		return // CNAME / managed targets — skip propagation check
	}
	expectedIP := binding.DNSTarget

	// Short grace period for DNS propagation.
	select {
	case <-time.After(dnsGracePeriod):
	case <-ctx.Done():
		return
	}

	unresolved := 0
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		for _, domain := range cfg.Domains[svcName] {
			// Resolve from the server — matches what curl will see in WaitForHTTPS.
			cmd := fmt.Sprintf("getent hosts %s 2>/dev/null", domain)
			result, err := ssh.Run(ctx, cmd)
			if err != nil {
				unresolved++
				continue
			}
			// getent output: "1.2.3.4    example.com"
			fields := strings.Fields(strings.TrimSpace(string(result)))
			if len(fields) == 0 || fields[0] != expectedIP {
				unresolved++
			}
		}
	}

	if unresolved > 0 {
		out.Warning(fmt.Sprintf(
			"%d domain(s) not resolving to %s yet — DNS propagation may be slow. "+
				"ACME certificate issuance will retry for up to 10 minutes, but may fail "+
				"if DNS does not propagate in time. If cert fails, redeploy.",
			unresolved, expectedIP))
	}
}
