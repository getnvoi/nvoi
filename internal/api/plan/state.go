package plan

import (
	"context"
	"strings"

	"github.com/getnvoi/nvoi/internal/api/config"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

// InfraStateRequest holds everything needed to query reality.
type InfraStateRequest struct {
	pkgcore.Cluster
	DNS     pkgcore.ProviderRef
	Storage pkgcore.ProviderRef
}

// InfraState queries providers and the cluster to build a Config representing
// what's currently deployed. Consumes pkg/core.Describe (cluster state) and
// provider ListServers/ListVolumes (provider state). Returns nil if nothing exists.
func InfraState(ctx context.Context, req InfraStateRequest) *Cfg {
	cfg := &Cfg{
		Servers:  map[string]config.Server{},
		Volumes:  map[string]config.Volume{},
		Services: map[string]config.Service{},
		Storage:  map[string]config.Storage{},
		Domains:  map[string]config.Domains{},
	}

	names, err := req.Names()
	if err != nil {
		return nil
	}
	prefix := names.Base() + "-"

	// ── Provider state: servers + volumes ──────────────────────────────────
	prov, err := req.Compute()
	if err != nil {
		return nil
	}

	servers, _ := prov.ListServers(ctx, names.Labels())
	for _, s := range servers {
		cfg.Servers[strings.TrimPrefix(s.Name, prefix)] = config.Server{}
	}

	volumes, _ := prov.ListVolumes(ctx, names.Labels())
	for _, v := range volumes {
		cfg.Volumes[strings.TrimPrefix(v.Name, prefix)] = config.Volume{}
	}

	// ── Cluster state: services, storage, DNS ─────────────────────────────
	desc, err := pkgcore.Describe(ctx, pkgcore.DescribeRequest{Cluster: req.Cluster})
	if err != nil {
		if len(cfg.Servers) == 0 && len(cfg.Volumes) == 0 {
			return nil
		}
		return cfg
	}

	for _, w := range desc.Workloads {
		cfg.Services[w.Name] = config.Service{}
	}
	for _, s := range desc.Storage {
		cfg.Storage[s.Name] = config.Storage{}
	}
	for _, i := range desc.Ingress {
		if existing, ok := cfg.Domains[i.Service]; ok {
			cfg.Domains[i.Service] = append(existing, i.Domain)
		} else {
			cfg.Domains[i.Service] = config.Domains{i.Domain}
		}
	}

	if len(cfg.Servers) == 0 && len(cfg.Services) == 0 {
		return nil
	}
	return cfg
}
