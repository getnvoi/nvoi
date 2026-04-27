package reconcile

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
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

// IsEmpty returns true when no entries were produced AT ALL. With
// PlanNoChange entries now emitted for every in-scope resource, this
// is essentially never true on a real cluster — use Changes() or
// Promptable() to check for actual diffs.
func (p *Plan) IsEmpty() bool { return len(p.Entries) == 0 }

// Changes returns the subset of entries that represent an actual diff
// (add / delete / update) — drops PlanNoChange. This is what
// reconcile.Deploy uses for its "No changes." early-exit and what the
// deploy preamble renders.
func (p *Plan) Changes() []provider.PlanEntry {
	out := make([]provider.PlanEntry, 0, len(p.Entries))
	for _, e := range p.Entries {
		if e.Status != provider.PlanNoChange {
			out = append(out, e)
		}
	}
	return out
}

// HasInfraChanges returns true when any CHANGE entry covers a
// provider-side resource. PlanNoChange entries are ignored — only
// real diffs trigger the loud/quiet path branch in Deploy.
func (p *Plan) HasInfraChanges() bool {
	for _, e := range p.Entries {
		if e.Status == provider.PlanNoChange {
			continue
		}
		if e.Kind.IsInfra() {
			return true
		}
	}
	return false
}

// Promptable returns the subset of entries that require user
// confirmation. PlanNoChange entries (no diff) and Reason-flagged
// entries (e.g. image-tag rebuilds) are filtered out.
func (p *Plan) Promptable() []provider.PlanEntry {
	out := make([]provider.PlanEntry, 0, len(p.Entries))
	for _, e := range p.Entries {
		if e.Status == provider.PlanNoChange {
			continue
		}
		if e.Promptable() {
			out = append(out, e)
		}
	}
	return out
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

	// Cluster.Kube routes to infra.Connect (read-only ≤500ms) when
	// MasterKube isn't already set — the same on-demand path every
	// other CLI verb uses. When ComputePlan runs from inside Deploy,
	// MasterKube is already populated from Bootstrap and Kube returns
	// the borrowed reference (cleanup is a no-op). Either way, we own
	// the client lifecycle here so the CLI doesn't touch the on-demand
	// fields directly.
	kc, _, cleanup, err := dc.Cluster.Kube(ctx, config.NewView(cfg))
	if err != nil {
		return nil, fmt.Errorf("plan: kube: %w", err)
	}
	defer cleanup()
	prevKube := dc.Cluster.MasterKube
	dc.Cluster.MasterKube = kc
	defer func() { dc.Cluster.MasterKube = prevKube }()

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

	// Storage buckets — diff cfg.Storage vs live BucketProvider.ListBuckets
	// (filtered by cluster prefix; orphan-safe with database backup buckets
	// included in the desired set).
	if dc.Storage.Name != "" {
		storageEntries, err := planStorage(ctx, dc, cfg)
		if err != nil {
			return nil, fmt.Errorf("plan: storage: %w", err)
		}
		plan.Entries = append(plan.Entries, storageEntries...)
	}

	// Databases: k8s-side existence (StatefulSet for selfhosted,
	// credentials Secret, backup CronJob, backup-creds Secret).
	dbEntries, err := planDatabases(ctx, dc, cfg)
	if err != nil {
		return nil, fmt.Errorf("plan: databases: %w", err)
	}
	plan.Entries = append(plan.Entries, dbEntries...)

	// Registries: pull-secret existence in the app namespace.
	regEntries, err := planRegistries(ctx, dc, cfg)
	if err != nil {
		return nil, fmt.Errorf("plan: registries: %w", err)
	}
	plan.Entries = append(plan.Entries, regEntries...)

	// Services + Crons: workload existence + image-tag detection +
	// per-workload Secret key diff.
	svcEntries, err := planServices(ctx, dc, cfg)
	if err != nil {
		return nil, fmt.Errorf("plan: services: %w", err)
	}
	plan.Entries = append(plan.Entries, svcEntries...)

	cronEntries, err := planCrons(ctx, dc, cfg)
	if err != nil {
		return nil, fmt.Errorf("plan: crons: %w", err)
	}
	plan.Entries = append(plan.Entries, cronEntries...)

	// DNS records — gated on Caddy mode (no tunnel) + infra exposing
	// public ingress + cfg declaring domains, mirroring Deploy's
	// own gate. In tunnel mode, DNS is CNAMEs written by TunnelIngress
	// (planTunnelIngress below); ListBindings only returns A/AAAA so
	// running planRouteDomains here would emit phantom ADDs for every
	// tunnel-routed domain on every plan.
	if cfg.Providers.Tunnel == "" && infra.HasPublicIngress() && len(cfg.Domains) > 0 {
		dnsEntries, err := planRouteDomains(ctx, dc, cfg)
		if err != nil {
			return nil, fmt.Errorf("plan: dns: %w", err)
		}
		plan.Entries = append(plan.Entries, dnsEntries...)
	}

	// Ingress: Caddy bootstrap workloads + per-domain routes (when
	// in Caddy mode), OR tunnel agent workloads + provider-side tunnel
	// (when providers.tunnel is set).
	if cfg.Providers.Tunnel != "" {
		tunEntries, err := planTunnelIngress(ctx, dc, cfg)
		if err != nil {
			return nil, fmt.Errorf("plan: tunnel: %w", err)
		}
		plan.Entries = append(plan.Entries, tunEntries...)
	} else if infra.HasPublicIngress() && len(cfg.Domains) > 0 {
		ingressEntries, err := planIngress(ctx, dc, cfg)
		if err != nil {
			return nil, fmt.Errorf("plan: ingress: %w", err)
		}
		plan.Entries = append(plan.Entries, ingressEntries...)
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

	existing, err := provider.ListOwned(ctx, kc, ns, provider.KindRegistrySecret, kube.KindSecret)
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
			Status: provider.PlanAdd,
			Kind:   provider.KindRegistrySecret,
			Name:   kube.PullSecretName,
			Detail: fmt.Sprintf("%d host(s)", len(cfg.Registry)),
		}}, nil
	case !wantSecret && hasSecret:
		return []provider.PlanEntry{{
			Status: provider.PlanDelete,
			Kind:   provider.KindRegistrySecret,
			Name:   kube.PullSecretName,
		}}, nil
	case wantSecret && hasSecret:
		return []provider.PlanEntry{{
			Status: provider.PlanNoChange,
			Kind:   provider.KindRegistrySecret,
			Name:   kube.PullSecretName,
			Detail: fmt.Sprintf("%d host(s)", len(cfg.Registry)),
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

	// Adds + unchanged: walk every declared domain.
	desiredSorted := sortedKeys(desired)
	for _, d := range desiredSorted {
		if liveDomains[d] {
			entries = append(entries, provider.PlanEntry{
				Status: provider.PlanNoChange, Kind: provider.KindDNSRecord, Name: d,
			})
			continue
		}
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanAdd, Kind: provider.KindDNSRecord, Name: d,
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
				Status: provider.PlanDelete,
				Kind:   provider.KindDNSRecord,
				Name:   d,
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

// imageHashRE matches the trailing `<sep>YYYYMMDD-HHMMSS` deploy-hash
// suffix that ResolveImage stamps onto every built image — `<sep>` is
// `:` when the user-declared image had no tag (`repo:<hash>`) and `-`
// when it had one (`repo:v2-<hash>`). Stripping this suffix from both
// live and desired images lets us tell whether the only diff is the
// hash (image-tag UPDATE that auto-applies) vs a real repo/tag change
// (full UPDATE that prompts).
var imageHashRE = regexp.MustCompile(`[-:]\d{8}-\d{6}$`)

// stripDeployHash removes the trailing `-YYYYMMDD-HHMMSS` segment if
// present. Idempotent on images that don't carry one.
func stripDeployHash(image string) string {
	return imageHashRE.ReplaceAllString(image, "")
}

// planServices diffs cfg.Services against the live cluster:
//
//   - Workload existence (Deployment / StatefulSet) via ListOwned + cfg.
//   - Image change detection per existing workload — image-tag-only
//     updates (the every-deploy case) carry Reason="image-tag" so
//     they auto-apply; any other repo/tag change is a full UPDATE.
//   - Per-service Secret key diff — keys added/removed in the
//     `<name>-secrets` Secret surface as Resource=ResSecretKey entries.
//
// Spec drift beyond image (replicas / env / port / command) currently
// applies silently in the apply path; richer diff is a follow-up.
func planServices(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) ([]provider.PlanEntry, error) {
	kc := dc.Cluster.MasterKube
	if kc == nil {
		return nil, fmt.Errorf("planServices: no master kube client")
	}
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}
	ns := names.KubeNamespace()

	desired := map[string]bool{}
	for n := range cfg.Services {
		desired[n] = true
	}

	live, err := combinedWorkloadNames(ctx, kc, ns, provider.KindServiceWorkload)
	if err != nil {
		return nil, err
	}

	var entries []provider.PlanEntry

	// Per-service add/update/unchanged entries. Iterate in a stable order.
	for _, name := range utils.SortedKeys(cfg.Services) {
		if !live[name] {
			entries = append(entries, provider.PlanEntry{
				Status: provider.PlanAdd,
				Kind:   provider.KindServiceWorkload,
				Name:   name,
				Detail: "service",
			})
			continue
		}
		// changed flips to true when an UPDATE entry is emitted below
		// (real diff). When false at the end, we emit the PlanNoChange
		// baseline so the inventory shows every cfg service even when
		// nothing changed.
		changed := false
		// Workload exists — compare image. Two distinct outcomes:
		//
		//   image-tag-only diff (only the trailing -YYYYMMDD-HHMMSS
		//   deploy-hash differs): on `nvoi deploy` BuildImages already
		//   ran, the rebuild is real, the workload will roll → emit
		//   the auto-skip entry so the operator sees what's happening.
		//   On `nvoi plan` (DeployHash unset) NO rebuild happens — the
		//   command is read-only — so suppressing the entry avoids
		//   misleading "image rebuilt" output for a no-op operation.
		//
		//   non-tag diff (repo / registry host / user-tag changed):
		//   surface ALWAYS as a promptable UPDATE — the user actually
		//   edited cfg, plan must show that regardless of call context.
		//
		// We use a placeholder hash for plan-only path so ResolveImage
		// has a value to stamp; the suppression rule below catches the
		// false-positive that placeholder would otherwise produce.
		hash := dc.Cluster.DeployHash
		planOnly := hash == ""
		if planOnly {
			hash = "00000000-000000"
		}
		desiredImage, err := ResolveImage(cfg, name, hash)
		if err != nil {
			return nil, fmt.Errorf("services.%s: resolve image: %w", name, err)
		}
		liveImage, err := getDeploymentOrSTSImage(ctx, kc, ns, name)
		if err != nil {
			return nil, fmt.Errorf("services.%s: read live image: %w", name, err)
		}
		if liveImage != "" && liveImage != desiredImage {
			if stripDeployHash(liveImage) == stripDeployHash(desiredImage) {
				if !planOnly {
					entries = append(entries, provider.PlanEntry{
						Status: provider.PlanUpdate,
						Kind:   provider.KindServiceWorkload,
						Name:   name,
						Detail: "image rebuilt",
						Reason: "image-tag",
					})
					changed = true
				}
				// planOnly: no rebuild; falls through to the
				// PlanNoChange emission below so the inventory still
				// shows the service with status=unchanged.
			} else {
				entries = append(entries, provider.PlanEntry{
					Status: provider.PlanUpdate,
					Kind:   provider.KindServiceWorkload,
					Name:   name,
					Detail: fmt.Sprintf("image: %s → %s", liveImage, desiredImage),
				})
				changed = true
			}
		}
		if !changed {
			entries = append(entries, provider.PlanEntry{
				Status: provider.PlanNoChange, Kind: provider.KindServiceWorkload,
				Name: name, Detail: "service",
			})
		}
	}

	// Orphan workloads: in live but not in cfg.
	liveNames := make([]string, 0, len(live))
	for n := range live {
		liveNames = append(liveNames, n)
	}
	sort.Strings(liveNames)
	for _, n := range liveNames {
		if desired[n] {
			continue
		}
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanDelete, Kind: provider.KindServiceWorkload, Name: n, Detail: "service",
		})
	}

	// Per-service Secret key diff — only for services in cfg whose
	// `<name>-secrets` already exists. Adds/removes flagged per-key.
	for _, name := range utils.SortedKeys(cfg.Services) {
		if !live[name] {
			continue // ADD entry above already covers initial keys
		}
		svc := cfg.Services[name]
		desiredKeys := desiredSecretKeys(svc.Secrets, svc.Storage, svc.Databases)
		liveKeys, err := kc.ListSecretKeys(ctx, ns, names.KubeServiceSecrets(name))
		if err != nil {
			continue // best-effort; treat as no diff
		}
		entries = append(entries, secretKeyDiff(name, desiredKeys, liveKeys)...)
	}

	return entries, nil
}

// planCrons mirrors planServices for `cfg.Crons` — workload existence
// + image-tag detection + schedule diff + per-cron Secret key diff.
func planCrons(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) ([]provider.PlanEntry, error) {
	kc := dc.Cluster.MasterKube
	if kc == nil {
		return nil, fmt.Errorf("planCrons: no master kube client")
	}
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}
	ns := names.KubeNamespace()

	liveNames, err := provider.ListOwned(ctx, kc, ns, provider.KindCronWorkload, kube.KindCronJob)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, n := range liveNames {
		live[n] = true
	}
	desired := map[string]bool{}
	for n := range cfg.Crons {
		desired[n] = true
	}

	var entries []provider.PlanEntry
	for _, name := range utils.SortedKeys(cfg.Crons) {
		if !live[name] {
			entries = append(entries, provider.PlanEntry{
				Status: provider.PlanAdd, Kind: provider.KindCronWorkload, Name: name,
				Detail: cfg.Crons[name].Schedule,
			})
			continue
		}
		// changed flips on any UPDATE entry below; when false at end,
		// emit the PlanNoChange baseline.
		changed := false
		// Schedule + image diff via direct CronJob Get.
		cj, err := kc.Clientset().BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			if cj.Spec.Schedule != cfg.Crons[name].Schedule {
				entries = append(entries, provider.PlanEntry{
					Status: provider.PlanUpdate, Kind: provider.KindCronWorkload, Name: name,
					Detail: fmt.Sprintf("schedule: %s → %s", cj.Spec.Schedule, cfg.Crons[name].Schedule),
				})
				changed = true
			}
			if len(cj.Spec.JobTemplate.Spec.Template.Spec.Containers) > 0 {
				liveImage := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image
				desiredImage := cfg.Crons[name].Image
				if liveImage != desiredImage && liveImage != "" {
					if stripDeployHash(liveImage) == stripDeployHash(desiredImage) {
						// Same suppression as planServices.
						if dc.Cluster.DeployHash != "" {
							entries = append(entries, provider.PlanEntry{
								Status: provider.PlanUpdate, Kind: provider.KindCronWorkload, Name: name,
								Detail: "image rebuilt", Reason: "image-tag",
							})
							changed = true
						}
					} else {
						entries = append(entries, provider.PlanEntry{
							Status: provider.PlanUpdate, Kind: provider.KindCronWorkload, Name: name,
							Detail: fmt.Sprintf("image: %s → %s", liveImage, desiredImage),
						})
						changed = true
					}
				}
			}
		}
		if !changed {
			entries = append(entries, provider.PlanEntry{
				Status: provider.PlanNoChange, Kind: provider.KindCronWorkload, Name: name,
				Detail: cfg.Crons[name].Schedule,
			})
		}
	}
	for _, n := range liveNames {
		if desired[n] {
			continue
		}
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanDelete, Kind: provider.KindCronWorkload, Name: n,
		})
	}
	for _, name := range utils.SortedKeys(cfg.Crons) {
		if !live[name] {
			continue
		}
		cr := cfg.Crons[name]
		// Crons don't have a `databases:` field today (per CronDef
		// shape); pass nil for that source. Storage + secrets follow
		// the same expansion rules as services.
		desiredKeys := desiredSecretKeys(cr.Secrets, cr.Storage, nil)
		liveKeys, err := kc.ListSecretKeys(ctx, ns, names.KubeServiceSecrets(name))
		if err != nil {
			continue
		}
		entries = append(entries, secretKeyDiff(name, desiredKeys, liveKeys)...)
	}
	return entries, nil
}

// planStorage diffs cfg.Storage (user-declared buckets) + the database
// backup buckets against the live BucketProvider listing scoped to the
// cluster prefix. Both surfaces share one bucket provider — we union
// the desired sets here so neither flags the other as orphan.
func planStorage(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) ([]provider.PlanEntry, error) {
	bucket, err := provider.ResolveBucket(dc.Storage.Name, dc.Storage.Creds)
	if err != nil {
		return nil, fmt.Errorf("resolve bucket provider: %w", err)
	}
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}
	prefix := names.Base() + "-"

	desired := map[string]bool{}
	for n, def := range cfg.Storage {
		want := def.Bucket
		if want == "" {
			want = names.Bucket(n)
		}
		desired[want] = true
	}
	for n, db := range cfg.Databases {
		if db.Backup != nil {
			desired[names.KubeDatabaseBackupBucket(n)] = true
		}
	}

	live, err := bucket.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}
	liveSet := map[string]bool{}
	for _, b := range live {
		if !strings.HasPrefix(b, prefix) {
			continue
		}
		liveSet[b] = true
	}

	var entries []provider.PlanEntry
	for _, n := range sortedKeys(desired) {
		if liveSet[n] {
			entries = append(entries, provider.PlanEntry{
				Status: provider.PlanNoChange, Kind: provider.KindBucket, Name: n,
			})
			continue
		}
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanAdd, Kind: provider.KindBucket, Name: n,
		})
	}
	// No DELETE entries: Storage() never deletes buckets (user data —
	// only `nvoi teardown --delete-storage` does). Emitting a delete
	// here would lie about what `nvoi deploy` actually does AND
	// inflate the prompt's "N to delete" count for changes that won't
	// happen. Stale buckets are reported by `nvoi resources` (with
	// the Owned/External classifier) instead.
	return entries, nil
}

// planDatabases diffs cfg.Databases against k8s-side state — credentials
// Secret existence, StatefulSet existence (selfhosted only), backup
// CronJob existence (when backup configured), backup-creds Secret
// existence. Provider-side resources for SaaS engines (Neon branches,
// PlanetScale databases) are NOT diffed here — that requires per-engine
// list APIs and is deferred until those are added to DatabaseProvider.
//
// The resulting entries report Resource=ResDatabase for the high-level
// "database X" change so the renderer doesn't drown the operator in
// the seven sub-resources each DB owns.
func planDatabases(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) ([]provider.PlanEntry, error) {
	kc := dc.Cluster.MasterKube
	if kc == nil {
		return nil, nil // first deploy — nothing to read; ADD entries via cfg-only loop
	}
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}
	ns := names.KubeNamespace()

	// Live: every credentials Secret carrying owner=databases tells us a
	// database currently exists. Suffix-match to derive the DB name.
	liveSecrets, err := provider.ListOwned(ctx, kc, ns, provider.KindDatabase, kube.KindSecret)
	if err != nil {
		return nil, err
	}
	liveDBs := map[string]bool{}
	credSuffix := "-credentials"
	for _, s := range liveSecrets {
		// names.KubeDatabaseCredentials(name) = base + "-db-" + name + "-credentials"
		marker := "-db-"
		if i := strings.Index(s, marker); i > 0 && strings.HasSuffix(s, credSuffix) {
			dbName := s[i+len(marker) : len(s)-len(credSuffix)]
			if dbName != "" {
				liveDBs[dbName] = true
			}
		}
	}

	desired := map[string]bool{}
	for n := range cfg.Databases {
		desired[n] = true
	}

	var entries []provider.PlanEntry
	for _, n := range utils.SortedKeys(cfg.Databases) {
		def := cfg.Databases[n]
		detail := def.Engine
		if def.Server != "" {
			detail = fmt.Sprintf("%s on %s", def.Engine, def.Server)
		}
		if !liveDBs[n] {
			entries = append(entries, provider.PlanEntry{
				Status: provider.PlanAdd, Kind: provider.KindDatabase, Name: n, Detail: detail,
			})
			continue
		}
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanNoChange, Kind: provider.KindDatabase, Name: n, Detail: detail,
		})
	}
	for _, n := range sortedKeys(liveDBs) {
		if desired[n] {
			continue
		}
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanDelete, Kind: provider.KindDatabase, Name: n,
		})
	}
	return entries, nil
}

// planIngress diffs the Caddy bootstrap workloads + per-domain routes
// against cfg. Caller already gated on cfg.Domains > 0 + Caddy mode.
//
//   - Caddy itself (Deployment/Service/ConfigMap/PVC in kube-system) →
//     ADD if missing entirely; no entry when present (re-apply is
//     idempotent and silent in the apply path).
//   - Per-domain routes via GetCaddyRoutes — ADD when desired domain
//     not loaded, DELETE for orphan routes.
func planIngress(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) ([]provider.PlanEntry, error) {
	kc := dc.Cluster.MasterKube
	if kc == nil {
		return nil, fmt.Errorf("planIngress: no master kube client")
	}

	// Caddy bootstrap presence — Deployment in kube-system w/ owner=caddy.
	caddyDeploys, err := provider.ListOwned(ctx, kc, kube.CaddyNamespace, provider.KindCaddyIngress, kube.KindDeployment)
	if err != nil {
		return nil, err
	}
	var entries []provider.PlanEntry
	caddyDetail := "ingress controller (kube-system)"
	if len(caddyDeploys) == 0 {
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanAdd, Kind: provider.KindCaddyIngress, Name: "caddy",
			Detail: caddyDetail,
		})
	} else {
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanNoChange, Kind: provider.KindCaddyIngress, Name: "caddy",
			Detail: caddyDetail,
		})
	}

	// Per-domain routes — graceful no-op when Caddy isn't up yet.
	live := map[string]bool{}
	if routes, err := kc.GetCaddyRoutes(ctx); err == nil {
		for _, r := range routes {
			for _, d := range r.Domains {
				live[d] = true
			}
		}
	}
	desired := map[string]bool{}
	for _, doms := range cfg.Domains {
		for _, d := range doms {
			desired[d] = true
		}
	}
	for _, d := range sortedKeys(desired) {
		if live[d] {
			entries = append(entries, provider.PlanEntry{
				Status: provider.PlanNoChange, Kind: provider.KindCaddyIngress, Name: d,
			})
			continue
		}
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanAdd, Kind: provider.KindCaddyIngress, Name: d,
		})
	}
	for _, d := range sortedKeys(live) {
		if desired[d] {
			continue
		}
		entries = append(entries, provider.PlanEntry{
			Status: provider.PlanDelete, Kind: provider.KindCaddyIngress, Name: d,
		})
	}
	return entries, nil
}

// planTunnelIngress diffs the tunnel agent k8s workloads + (TODO)
// provider-side tunnel object. Today we only diff the k8s side — adding
// or removing `providers.tunnel:` from cfg flips the whole stack, so a
// presence/absence check on the agent Deployment captures the user-
// visible delta. Provider-side tunnel object listing is deferred to a
// follow-up that adds a List method to TunnelProvider.
func planTunnelIngress(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) ([]provider.PlanEntry, error) {
	kc := dc.Cluster.MasterKube
	if kc == nil {
		return nil, fmt.Errorf("planTunnelIngress: no master kube client")
	}
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}
	ns := names.KubeNamespace()
	agents, err := provider.ListOwned(ctx, kc, ns, provider.KindTunnelAgent, kube.KindDeployment)
	if err != nil {
		return nil, err
	}
	if len(agents) == 0 && cfg.Providers.Tunnel != "" {
		return []provider.PlanEntry{{
			Status: provider.PlanAdd,
			Kind:   provider.KindTunnelAgent,
			Name:   cfg.Providers.Tunnel,
			Detail: "tunnel agent + provider-side tunnel",
		}}, nil
	}
	if len(agents) > 0 && cfg.Providers.Tunnel == "" {
		return []provider.PlanEntry{{
			Status: provider.PlanDelete,
			Kind:   provider.KindTunnelAgent,
			Name:   "tunnel",
		}}, nil
	}
	if len(agents) > 0 && cfg.Providers.Tunnel != "" {
		return []provider.PlanEntry{{
			Status: provider.PlanNoChange,
			Kind:   provider.KindTunnelAgent,
			Name:   cfg.Providers.Tunnel,
			Detail: "tunnel agent + provider-side tunnel",
		}}, nil
	}
	return nil, nil
}

// ── shared helpers used by the workload planners ──────────────────────

// combinedWorkloadNames returns the union of Deployment + StatefulSet
// names for the given Kind. Services emit one of the two depending on
// whether they declare `volumes:` (StatefulSet) or not (Deployment).
func combinedWorkloadNames(ctx context.Context, kc *kube.Client, ns string, kind provider.Kind) (map[string]bool, error) {
	deps, err := provider.ListOwned(ctx, kc, ns, kind, kube.KindDeployment)
	if err != nil {
		return nil, err
	}
	stsNames, err := provider.ListOwned(ctx, kc, ns, kind, kube.KindStatefulSet)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, n := range deps {
		out[n] = true
	}
	for _, n := range stsNames {
		out[n] = true
	}
	return out, nil
}

// getDeploymentOrSTSImage reads the first container's image from
// either kind. Returns "" when neither exists or when the container
// list is empty (zero-state — apply will populate it).
func getDeploymentOrSTSImage(ctx context.Context, kc *kube.Client, ns, name string) (string, error) {
	cs := kc.Clientset()
	if dep, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
		if len(dep.Spec.Template.Spec.Containers) > 0 {
			return dep.Spec.Template.Spec.Containers[0].Image, nil
		}
	}
	if sts, err := cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
		if len(sts.Spec.Template.Spec.Containers) > 0 {
			return sts.Spec.Template.Spec.Containers[0].Image, nil
		}
	}
	return "", nil
}

// desiredSecretKeys mirrors what Services()/Crons() actually write into
// the per-workload `<name>-secrets` Secret. Three sources, all in one
// place so the diff matches reality:
//
//  1. Bare/aliased `secrets:` entries — `FOO` → `FOO`, `ALIAS=$VAR` → `ALIAS`.
//     Mirror of resolveSecretEntries.
//  2. Storage credentials — every `storage:` ref expands to the four
//     keys returned by app.StorageSecretKeys (ENDPOINT/BUCKET/AKID/SAK).
//     Mirror of expandStorageCreds.
//  3. Database URLs — every `databases:` ref expands to one
//     DATABASE_URL_<NAME> (or the user-aliased name when written as
//     `ALIAS=NAME`). Mirror of expandDatabaseCreds.
//
// Without (2) + (3), planServices/planCrons would false-flag every
// expansion key as an orphan DELETE on every deploy.
func desiredSecretKeys(secrets, storages, databases []string) []string {
	out := make([]string, 0, len(secrets)+len(storages)*4+len(databases))
	// (1) `secrets:` entries — bare or aliased.
	for _, e := range secrets {
		if i := strings.IndexByte(e, '='); i > 0 {
			out = append(out, e[:i])
		} else {
			out = append(out, e)
		}
	}
	// (2) `storage:` expansions.
	for _, s := range storages {
		out = append(out, app.StorageSecretKeys(s)...)
	}
	// (3) `databases:` expansions.
	for _, d := range databases {
		envName, dbName := parseDatabaseRef(d)
		if envName == "" {
			envName = utils.DatabaseEnvName(dbName)
		}
		out = append(out, envName)
	}
	return out
}

// secretKeyDiff emits per-key ADD / DELETE / NoChange entries for a
// workload's per-service Secret. Helps catch the "operator dropped a
// secret ref" case which would otherwise apply silently.
func secretKeyDiff(workload string, desired, live []string) []provider.PlanEntry {
	d := map[string]bool{}
	for _, k := range desired {
		d[k] = true
	}
	l := map[string]bool{}
	for _, k := range live {
		l[k] = true
	}
	var out []provider.PlanEntry
	for _, k := range desired {
		if l[k] {
			out = append(out, provider.PlanEntry{
				Status: provider.PlanNoChange, Kind: provider.KindSecretKey,
				Name: workload + ":" + k,
			})
			continue
		}
		out = append(out, provider.PlanEntry{
			Status: provider.PlanAdd, Kind: provider.KindSecretKey,
			Name: workload + ":" + k,
		})
	}
	for _, k := range live {
		if !d[k] {
			out = append(out, provider.PlanEntry{
				Status: provider.PlanDelete,
				Kind:   provider.KindSecretKey,
				Name:   workload + ":" + k,
			})
		}
	}
	return out
}

// silence unused-import if a later refactor drops one of these.
var _ = strings.TrimSpace
