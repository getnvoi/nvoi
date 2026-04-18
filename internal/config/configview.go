package config

import (
	"context"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// View wraps *AppConfig as a provider.ProviderConfigView so callers can
// hand a single object to InfraProvider.Bootstrap / LiveSnapshot /
// TeardownOrphans / Teardown / ValidateConfig without dragging
// internal/config into pkg/provider.
//
// Each accessor projects AppConfig into the small provider-facing shapes
// (ServerSpec / VolumeSpec / ServiceSpec). Sorted-by-name everywhere so
// every provider sees deterministic input order — matters for the
// reconcile equivalence test, which freezes byte-exact OpLog goldens.
type View struct {
	cfg *AppConfig
}

// NewView wraps cfg as a provider.ProviderConfigView.
func NewView(cfg *AppConfig) *View { return &View{cfg: cfg} }

func (v *View) AppName() string { return v.cfg.App }
func (v *View) EnvName() string { return v.cfg.Env }

func (v *View) ServerDefs() []provider.ServerSpec {
	out := make([]provider.ServerSpec, 0, len(v.cfg.Servers))
	for _, name := range utils.SortedKeys(v.cfg.Servers) {
		s := v.cfg.Servers[name]
		out = append(out, provider.ServerSpec{
			Name:   name,
			Type:   s.Type,
			Region: s.Region,
			Role:   s.Role,
			Disk:   s.Disk,
		})
	}
	return out
}

func (v *View) FirewallRules() []string {
	if len(v.cfg.Firewall) == 0 {
		return nil
	}
	cp := make([]string, len(v.cfg.Firewall))
	copy(cp, v.cfg.Firewall)
	return cp
}

func (v *View) VolumeDefs() []provider.VolumeSpec {
	out := make([]provider.VolumeSpec, 0, len(v.cfg.Volumes))
	for _, name := range utils.SortedKeys(v.cfg.Volumes) {
		vol := v.cfg.Volumes[name]
		out = append(out, provider.VolumeSpec{
			Name:      name,
			Size:      vol.Size,
			Server:    vol.Server,
			MountPath: vol.MountPath,
		})
	}
	return out
}

func (v *View) ServiceDefs() []provider.ServiceSpec {
	out := make([]provider.ServiceSpec, 0, len(v.cfg.Services))
	for _, name := range utils.SortedKeys(v.cfg.Services) {
		s := v.cfg.Services[name]
		out = append(out, provider.ServiceSpec{Name: name, Port: s.Port})
	}
	return out
}

func (v *View) DomainsByService() map[string][]string {
	if len(v.cfg.Domains) == 0 {
		return nil
	}
	cp := make(map[string][]string, len(v.cfg.Domains))
	for k, v := range v.cfg.Domains {
		dup := make([]string, len(v))
		copy(dup, v)
		cp[k] = dup
	}
	return cp
}

// BootstrapContext builds the provider.BootstrapContext the reconciler
// hands to InfraProvider methods. ProviderName falls back through:
//
//  1. dc.Cluster.Provider — already populated by the cmd/ boundary from
//     cfg.Providers.Infra / .Compute.
//  2. cfg.Providers.Infra — primary source if Cluster.Provider is unset.
//  3. cfg.Providers.Compute — legacy alias still accepted during the
//     staged rollout. C8 hard-removes it.
//
// The cascade lets tests set Provider directly on Cluster without
// populating cfg.Providers, while production wires both through cmd/.
func BootstrapContext(dc *DeployContext, cfg *AppConfig) *provider.BootstrapContext {
	name := dc.Cluster.Provider
	if name == "" {
		name = cfg.Providers.Infra
	}
	if name == "" {
		name = cfg.Providers.Compute
	}
	bctx := &provider.BootstrapContext{
		App:          dc.Cluster.AppName,
		Env:          dc.Cluster.Env,
		ProviderName: name,
		Credentials:  dc.Cluster.Credentials,
		SSHKey:       dc.Cluster.SSHKey,
		DeployHash:   dc.Cluster.DeployHash,
		Output:       dc.Cluster.Log(),
		Cfg:          NewView(cfg),
	}
	// Forward Cluster.SSHFunc as SSHDial so provider Bootstrap / Teardown
	// can be intercepted by test mocks. Production leaves SSHFunc nil and
	// providers fall back to infra.ConnectSSH.
	if dc.Cluster.SSHFunc != nil {
		ssh := dc.Cluster.SSHFunc
		bctx.SSHDial = func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return ssh(ctx, addr)
		}
	}
	// Forward pre-injected MasterKube (test scaffolding via convergeDC)
	// so provider Bootstrap returns the KubeFake instead of building a
	// real tunneled client. Production deploys leave MasterKube nil.
	bctx.MasterKube = dc.Cluster.MasterKube
	return bctx
}
