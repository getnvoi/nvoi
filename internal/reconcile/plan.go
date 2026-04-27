package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Plan is the aggregated diff between desired state (cfg) and live
// state across every reconcile step. Built by ComputePlan, consumed by
// the renderer + the deploy-time prompt logic.
//
// The Plan splits naturally into two domains:
//
//   - Infra entries (servers / firewalls / volumes / network / storage /
//     databases / dns / tunnel) — when this set is empty, reconcile.Deploy
//     can skip the loud per-resource ensure path and use Connect instead
//     of Bootstrap.
//
//   - Workload entries (registries / services / crons / caddy ingress) —
//     k8s reconcile always runs, but the plan still drives the prompt
//     logic for non-image-tag changes.
//
// The PlanEntry's Resource field carries the kind; HasInfraChanges /
// Promptable bucket the entries by their downstream consumer.
type Plan struct {
	Entries []provider.PlanEntry
}

// IsEmpty returns true when no entries were produced (converged across
// every step). Caller may shortcut to "No changes" output.
func (p *Plan) IsEmpty() bool { return len(p.Entries) == 0 }

// HasInfraChanges returns true when any entry covers a provider-side
// resource. Used by reconcile.Deploy to choose between Bootstrap (loud
// path) and Connect (quiet path). Workload-only deltas (Services /
// Crons / Caddy) leave infra unchanged → quiet path.
func (p *Plan) HasInfraChanges() bool {
	for _, e := range p.Entries {
		if isInfraResource(e.Resource) {
			return true
		}
	}
	return false
}

// Promptable returns the subset of entries that require user
// confirmation. Image-tag-only updates and any other Reason-flagged
// entries are filtered out (they apply silently).
func (p *Plan) Promptable() []provider.PlanEntry {
	out := make([]provider.PlanEntry, 0, len(p.Entries))
	for _, e := range p.Entries {
		if e.Promptable() {
			out = append(out, e)
		}
	}
	return out
}

// isInfraResource classifies a plan entry's Resource as provider-side
// (true) or workload-side (false). The split drives the loud/quiet
// path decision in reconcile.Deploy.
func isInfraResource(resource string) bool {
	switch resource {
	case provider.ResServer,
		provider.ResFirewall,
		provider.ResFirewallRule,
		provider.ResVolume,
		provider.ResNetwork,
		provider.ResDNS,
		provider.ResBucket,
		provider.ResDatabase,
		provider.ResTunnel:
		return true
	}
	return false
}

// ComputePlan walks every reconcile step's planner against live state
// and returns the aggregated Plan. Read-only — no provider mutations,
// no kube writes. Cheap by construction: each planner uses the same
// list/get primitives the apply path uses.
//
// Step ordering matches reconcile.Deploy's sequence so the renderer's
// output reads top-to-bottom in the same order the apply phase would
// emit. Errors from individual planners abort the whole computation —
// a planner that can't read live state can't tell us whether changes
// are safe.
//
// Phase 2a scope: infra (via InfraProvider.PlanInfra), registries, and
// DNS. Services / Crons / Storage / Databases / Ingress / Tunnel
// planners land in subsequent commits — their absence here means the
// returned Plan is incomplete for those domains until then.
func ComputePlan(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) (*Plan, error) {
	plan := &Plan{}

	// Infra: provider-owned diff (servers / firewalls / volumes).
	// Resolve the provider read-only — credentials come from the same
	// CredentialSource the deploy path uses.
	bctx := config.BootstrapContext(dc, cfg)
	infra, err := provider.ResolveInfra(bctx.ProviderName, dc.Cluster.Credentials)
	if err != nil {
		return nil, fmt.Errorf("plan: resolve infra: %w", err)
	}
	defer func() { _ = infra.Close() }()
	infraEntries, err := infra.PlanInfra(ctx, bctx)
	if err != nil {
		return nil, fmt.Errorf("plan: infra: %w", err)
	}
	plan.Entries = append(plan.Entries, infraEntries...)

	// Registries: pull-secret existence in the app namespace.
	regEntries, err := planRegistries(ctx, dc, cfg)
	if err != nil {
		return nil, fmt.Errorf("plan: registries: %w", err)
	}
	plan.Entries = append(plan.Entries, regEntries...)

	// DNS records — gated on infra exposing public ingress + cfg
	// declaring domains, mirroring reconcile.Deploy's gate.
	if infra.HasPublicIngress() && len(cfg.Domains) > 0 {
		dnsEntries, err := planRouteDomains(ctx, dc, cfg)
		if err != nil {
			return nil, fmt.Errorf("plan: dns: %w", err)
		}
		plan.Entries = append(plan.Entries, dnsEntries...)
	}

	return plan, nil
}

// planRegistries diffs the pull-secret existence in the app namespace
// against cfg.Registry. The semantics mirror Registries() exactly:
//
//   - cfg has registry entries, secret missing  → ADD
//   - cfg has no registry entries, secret present → DELETE (orphan
//     scrub matches Registries' explicit DeleteSecret call)
//   - both present, both absent                  → no entry
//
// We don't diff credential CONTENTS — secret-key rotation surfaces in
// planSecrets / per-service secret diffs once those land. The pull
// secret's payload is a single dockerconfigjson blob; key-level diff
// isn't meaningful.
func planRegistries(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) ([]provider.PlanEntry, error) {
	kc := dc.Cluster.MasterKube
	if kc == nil {
		return nil, fmt.Errorf("planRegistries: no master kube client")
	}
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}
	ns := names.KubeNamespace()

	existing, err := kc.ListOwned(ctx, ns, utils.OwnerRegistries, kube.KindSecret)
	if err != nil {
		return nil, err
	}
	hasSecret := false
	for _, name := range existing {
		if name == kube.PullSecretName {
			hasSecret = true
			break
		}
	}

	wantSecret := len(cfg.Registry) > 0
	switch {
	case wantSecret && !hasSecret:
		return []provider.PlanEntry{{
			Kind:     provider.PlanAdd,
			Resource: provider.ResRegistrySecret,
			Name:     kube.PullSecretName,
			Detail:   fmt.Sprintf("%d host(s)", len(cfg.Registry)),
		}}, nil
	case !wantSecret && hasSecret:
		return []provider.PlanEntry{{
			Kind:     provider.PlanDelete,
			Resource: provider.ResRegistrySecret,
			Name:     kube.PullSecretName,
		}}, nil
	}
	return nil, nil
}

// planRouteDomains diffs cfg.Domains against the live DNS records for
// the configured zone. Each (service, domain) pair in cfg becomes
// either an ADD (no live record) or a no-op (record present); each
// live record matching a previously-routed domain that isn't in cfg
// becomes a DELETE.
//
// Target value (master IP) drift is intentionally NOT diffed at the
// DNS layer here: a master IP change implies a server-replacement
// entry from PlanInfra, which already routes the deploy through the
// loud path. Re-running RouteDomains during apply will overwrite the
// stale target.
//
// Gating: caller already checked infra.HasPublicIngress() and
// len(cfg.Domains) > 0 before invoking us, mirroring Deploy's gate.
func planRouteDomains(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) ([]provider.PlanEntry, error) {
	if dc.DNS.Name == "" {
		// No DNS provider configured but Deploy() would have gated on
		// HasPublicIngress + len(Domains) > 0 + tunnel-or-dns. If we got
		// here without a DNS provider, ValidateConfig already would
		// have caught it. Defensive: treat as nothing to plan.
		return nil, nil
	}
	dns, err := provider.ResolveDNS(dc.DNS.Name, dc.DNS.Creds)
	if err != nil {
		return nil, fmt.Errorf("resolve dns provider: %w", err)
	}

	desired := map[string]bool{}
	for _, doms := range cfg.Domains {
		for _, d := range doms {
			desired[d] = true
		}
	}

	live, err := dns.ListBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("dns.ListBindings: %w", err)
	}
	liveDomains := map[string]bool{}
	for _, b := range live {
		liveDomains[b.Domain] = true
	}

	var entries []provider.PlanEntry

	// Adds: declared domains with no matching live record.
	desiredSorted := sortedKeys(desired)
	for _, d := range desiredSorted {
		if liveDomains[d] {
			continue
		}
		entries = append(entries, provider.PlanEntry{
			Kind:     provider.PlanAdd,
			Resource: provider.ResDNS,
			Name:     d,
		})
	}

	// Deletes: live records that aren't in cfg.Domains. We can't tell
	// from the binding alone whether a record was nvoi-managed or
	// hand-rolled in the operator's DNS console. The Caddy live route
	// table is the only source of truth for "was this nvoi's"; that
	// check lives in RouteDomains' apply path (queries
	// kc.GetCaddyRoutes). For plan output, list everything we'd
	// unroute — operator can spot a hand-rolled record and back out.
	if routes, err := dc.Cluster.MasterKube.GetCaddyRoutes(ctx); err == nil {
		caddyManaged := map[string]bool{}
		for _, r := range routes {
			for _, d := range r.Domains {
				caddyManaged[d] = true
			}
		}
		for _, d := range sortedDomains(liveDomains) {
			if desired[d] {
				continue
			}
			if !caddyManaged[d] {
				continue // not nvoi's, leave alone
			}
			entries = append(entries, provider.PlanEntry{
				Kind:     provider.PlanDelete,
				Resource: provider.ResDNS,
				Name:     d,
			})
		}
	}

	return entries, nil
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedDomains(m map[string]bool) []string { return sortedKeys(m) }

// silence unused-import if a later refactor drops one of these.
var _ = strings.TrimSpace
