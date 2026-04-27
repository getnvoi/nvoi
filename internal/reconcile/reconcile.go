package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// DeployOpts controls Deploy's prompt + plan-output behavior. Zero
// value = no callback = proceed silently (test default). Production
// CLI passes WithOnPlan to render the plan + prompt the operator.
type DeployOpts struct {
	// OnPlan, when non-nil, is called once the cfg-vs-live diff has
	// been computed (and is non-empty). The callback returns
	// (true, nil) to proceed with apply, (false, nil) to abort
	// cleanly (Deploy returns nil), or (_, err) to fail the deploy.
	// The callback owns rendering AND the prompt — engine stays UI-
	// free.
	OnPlan func(plan *Plan) (proceed bool, err error)
}

// Option mutates DeployOpts via the functional-options pattern so
// adding new knobs later is non-breaking.
type Option func(*DeployOpts)

// WithOnPlan registers the OnPlan callback. CLI passes a closure that
// renders the plan and prompts; tests pass nothing and proceed silently.
func WithOnPlan(fn func(*Plan) (bool, error)) Option {
	return func(o *DeployOpts) { o.OnPlan = fn }
}

// Deploy reconciles live infrastructure to match the YAML config.
//
// Phase 2 flow:
//
//  1. ValidateConfig + cfg.Resolve()
//  2. Stamp DeployHash
//  3. Build local images (pre-infra; failure aborts before any provisioning)
//  4. ComputePlan against live state (read-only)
//     - Empty plan → "No changes." early return.
//     - Non-empty → call opts.OnPlan; abort if operator says no.
//  5. Loud-path branch (plan.HasInfraChanges()):
//     - true: infra.Bootstrap → drift reconciled, full per-resource output
//     - false: infra.Connect → read-only attach, no provider mutation
//  6. EnsureNamespace + Registries + Secrets
//  7. Storage + Databases (loud path only — provider-side is silent
//     in steady state; quiet path skips entirely)
//  8. Services + Crons (always — image-tag updates land here)
//  9. infra.TeardownOrphans (loud path only)
//  10. Ingress (gated, loud path only for RouteDomains; Caddy reload
//     always when domains present)
//
// The reconciler never branches on "what kind of provider is this" —
// gates (HasPublicIngress, returned-nil NodeShell, ConsumesBlocks)
// carry every distinction.
func Deploy(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, opts ...Option) error {
	deployOpts := DeployOpts{}
	for _, opt := range opts {
		opt(&deployOpts)
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if err := cfg.Resolve(); err != nil {
		return err
	}

	// Sync cfg.Providers.Infra onto Cluster.Provider so legacy pkg/core
	// helpers that resolve the provider via Cluster see the right name.
	if dc.Cluster.Provider == "" {
		dc.Cluster.Provider = cfg.Providers.Infra
	}

	// Per-deploy hash stamps built image tags AND every workload's
	// metadata + pod template. Format is a sortable UTC timestamp down
	// to the second — unique per `bin/deploy` run.
	dc.Cluster.DeployHash = time.Now().UTC().Format("20060102-150405")

	// Resolve the infra provider before Build — ArchForType is pure (no
	// API calls, no credentials consumed) and we need the platform string
	// to stamp --platform on every docker buildx invocation.
	bctx := config.BootstrapContext(dc, cfg)
	infra, err := provider.ResolveInfra(bctx.ProviderName, dc.Cluster.Credentials)
	if err != nil {
		return fmt.Errorf("resolve infra provider: %w", err)
	}
	defer func() { _ = infra.Close() }()

	// Derive build platform from the master server type. Ensures the built
	// image arch always matches the target server — critical when the
	// operator builds on amd64 but deploys to an arm64 node (e.g. cax11).
	buildPlatform := "linux/" + infra.ArchForType(masterServerType(cfg))

	// Some BuildProviders (ssh) require role: builder servers to exist
	// before BuildImages fires. Provision them first, then hand the SSH
	// targets to BuildImages via the builders slice. Providers that don't
	// need builders (local, daytona) are declared RequiresBuilders=false
	// — ProvisionBuilders is still called but is a no-op on those backends
	// when no role: builder server is declared (the validator R1 rule
	// already forced consistency).
	buildName := cfg.Providers.Build
	if buildName == "" {
		buildName = "local"
	}
	caps, err := provider.GetBuildCapability(buildName)
	if err != nil {
		return fmt.Errorf("resolve build capability: %w", err)
	}
	var builders []provider.BuilderTarget
	if caps.RequiresBuilders {
		if err := infra.ProvisionBuilders(ctx, bctx); err != nil {
			return fmt.Errorf("provision builders: %w", err)
		}
		t, err := infra.BuilderTargets(ctx, bctx)
		if err != nil {
			return fmt.Errorf("builder targets: %w", err)
		}
		builders = t
	}

	// Build images for services with `build:` declared BEFORE touching
	// the rest of the infra. A build failure should never leave us with
	// half-provisioned servers. BuildImages resolves the BuildProvider
	// (local/ssh/daytona) and calls bp.Build once per service.
	if err := BuildImages(ctx, dc, cfg, buildPlatform, builders); err != nil {
		return err
	}

	// Compute the cfg-vs-live diff. Read-only — uses Connect under the
	// hood. Drives both the prompt (OnPlan callback) and the loud/quiet
	// path branch (Bootstrap vs Connect for the apply phase).
	plan, err := ComputePlan(ctx, dc, cfg)
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	if len(plan.Changes()) == 0 {
		dc.Cluster.Log().Success("No changes.")
		return nil
	}
	if deployOpts.OnPlan != nil {
		proceed, err := deployOpts.OnPlan(plan)
		if err != nil {
			return err
		}
		if !proceed {
			dc.Cluster.Log().Info("aborted")
			return nil
		}
	}

	// Loud-path branch: infra.Bootstrap reconciles drift (servers /
	// firewall rules / volumes) AND emits per-resource output. When no
	// infra entry sits in the plan, infra.Connect attaches read-only
	// without the per-resource churn — same *kube.Client, no output
	// noise, no provider mutations.
	loud := plan.HasInfraChanges()
	var kc *kube.Client
	if loud {
		kc, err = infra.Bootstrap(ctx, bctx)
		if err != nil {
			return fmt.Errorf("infra.Bootstrap: %w", err)
		}
	} else {
		dc.Cluster.Log().Success("infra unchanged")
		kc, err = infra.Connect(ctx, bctx)
		if err != nil {
			return fmt.Errorf("infra.Connect: %w", err)
		}
	}
	defer kc.Close()
	dc.Cluster.MasterKube = kc

	// Optional node shell for `nvoi ssh` and any infra-internal helper
	// that wants to exec on the host. Providers without host shell return
	// (nil, nil); CLI feature-gates on nil.
	if ns, err := infra.NodeShell(ctx, bctx); err != nil {
		return fmt.Errorf("infra.NodeShell: %w", err)
	} else if ns != nil {
		dc.Cluster.NodeShell = ns
	}

	// App-namespace must exist before any per-service secret / workload
	// write — otherwise the first writer races and fails with "namespaces
	// not found."
	names, err := dc.Cluster.Names()
	if err != nil {
		return err
	}
	if err := kc.EnsureNamespace(ctx, names.KubeNamespace()); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}

	// Registry pull credentials must land before Services/Crons —
	// kubelet reads imagePullSecrets at first image pull.
	if err := Registries(ctx, dc, cfg); err != nil {
		return fmt.Errorf("registries: %w", err)
	}

	secretValues, err := Secrets(ctx, dc, cfg)
	if err != nil {
		return err
	}

	// Storage + Databases provider-side reconcile + provider-side
	// orphan sweep are infra concerns — only run them when the plan
	// flagged infra changes. On the quiet path we read existing
	// credentials back from the cluster so Services/Crons still get
	// DATABASE_URL_* + STORAGE_* for their per-service Secrets.
	var sources map[string]string
	var pendingMigrations []PendingMigration
	if loud {
		storageCreds, err := Storage(ctx, dc, cfg)
		if err != nil {
			return err
		}
		databaseCreds, pm, err := Databases(ctx, dc, cfg, secretValues)
		if err != nil {
			return err
		}
		pendingMigrations = pm
		sources = mergeSources(secretValues, storageCreds, databaseCreds)
	} else {
		storageCreds, err := readExistingStorageCreds(ctx, dc, cfg)
		if err != nil {
			return fmt.Errorf("read existing storage creds: %w", err)
		}
		databaseCreds, err := readExistingDatabaseURLs(ctx, dc, cfg)
		if err != nil {
			return fmt.Errorf("read existing database URLs: %w", err)
		}
		sources = mergeSources(secretValues, storageCreds, databaseCreds)
	}

	if err := Services(ctx, dc, cfg, sources); err != nil {
		return err
	}
	if err := Crons(ctx, dc, cfg, sources); err != nil {
		return err
	}

	// Provider-side orphan sweep: only meaningful when infra changed.
	// Quiet path leaves provider state alone by definition.
	if loud {
		if err := infra.TeardownOrphans(ctx, bctx); err != nil {
			return err
		}
	}

	// Ingress: tunnel providers (#49) replace Caddy entirely when
	// cfg.Providers.Tunnel is set. Otherwise, Caddy handles ingress when
	// the infra provider has a public IP and domains are configured.
	// On the quiet path, RouteDomains is skipped (DNS targets master IP
	// which can't have changed if no infra changed); Caddy reload still
	// runs because cfg.Domains may have changed even without infra.
	if cfg.Providers.Tunnel != "" {
		if err := TunnelIngress(ctx, dc, cfg); err != nil {
			return err
		}
	} else if infra.HasPublicIngress() && len(cfg.Domains) > 0 {
		if loud {
			if err := RouteDomains(ctx, dc, cfg, infra, bctx); err != nil {
				return err
			}
			verifyDNSPropagation(ctx, dc, cfg)
		}
		if err := Ingress(ctx, dc, cfg); err != nil {
			return err
		}
	}

	// Pending-migration summary — emitted LAST so it stays visible after
	// every other deploy event has scrolled past. Deploy itself exits 0
	// even with pending migrations: the old DB pod keeps serving from
	// its current node, consumer services stay connected via the pod-
	// agnostic k8s Service name, and the operator resolves drift
	// explicitly via `nvoi database migrate <name>` (#67).
	emitPendingMigrations(dc, pendingMigrations)
	return nil
}

// readExistingStorageCreds rebuilds the sources map fragment that
// Storage() would have produced, WITHOUT touching the bucket
// provider's "ensure" path or emitting per-storage output. Used on the
// quiet deploy path so Services/Crons still get their STORAGE_*
// env keys when no infra changed.
//
// Safe-by-default: a missing storage entry yields an empty cred set
// for that entry — Services' expandStorageCreds skips silently when
// keys aren't present, matching today's behavior on first deploy.
func readExistingStorageCreds(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) (map[string]string, error) {
	if dc.Storage.Name == "" || len(cfg.Storage) == 0 {
		return map[string]string{}, nil
	}
	bucket, err := provider.ResolveBucket(dc.Storage.Name, dc.Storage.Creds)
	if err != nil {
		return nil, err
	}
	creds, err := bucket.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for n, def := range cfg.Storage {
		bucketName := def.Bucket
		if bucketName == "" {
			bucketName = names.Bucket(n)
		}
		prefix := utils.StorageEnvPrefix(n)
		out[prefix+"_ENDPOINT"] = creds.Endpoint
		out[prefix+"_BUCKET"] = bucketName
		out[prefix+"_ACCESS_KEY_ID"] = creds.AccessKeyID
		out[prefix+"_SECRET_ACCESS_KEY"] = creds.SecretAccessKey
	}
	return out, nil
}

// readExistingDatabaseURLs returns DATABASE_URL_<NAME> for every
// database in cfg by reading the existing per-DB credentials Secret.
// Used on the quiet deploy path so Services/Crons that envFrom these
// URLs continue to resolve them in their per-service Secret rebuild.
//
// Safe-by-default: a missing credentials Secret means the database
// hasn't been provisioned yet — but if the plan didn't flag it as a
// change, we expect it to exist. A missing Secret here returns ""
// for that DB; downstream Services' expandDatabaseCreds will skip the
// key, matching the first-deploy behavior.
func readExistingDatabaseURLs(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) (map[string]string, error) {
	if dc.Cluster.MasterKube == nil || len(cfg.Databases) == 0 {
		return map[string]string{}, nil
	}
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name := range cfg.Databases {
		url, err := dc.Cluster.MasterKube.GetSecretValue(
			ctx, names.KubeNamespace(),
			names.KubeDatabaseCredentials(name), "url",
		)
		if err != nil {
			continue // missing — skip; downstream tolerates absent keys
		}
		out[utils.DatabaseEnvName(name)] = url
	}
	return out, nil
}

// kube import is load-bearing for the kc variable type hoisted out of
// the if/else above the switch. Without it the package doesn't build.
var _ = (*kube.Client)(nil)

func emitPendingMigrations(dc *config.DeployContext, pending []PendingMigration) {
	if len(pending) == 0 || dc.Cluster.Log() == nil {
		return
	}
	log := dc.Cluster.Log()
	log.Warning(fmt.Sprintf("Pending migrations (%d):", len(pending)))
	for _, p := range pending {
		log.Warning(fmt.Sprintf("    databases.%s     %s → %s", p.Database, p.From, p.To))
		log.Warning(fmt.Sprintf("    Run: nvoi database migrate %s", p.Database))
	}
}

// RouteDomains writes a DNS record per (service, domain) pair via the
// configured DNSProvider. The IngressBinding (DNS type + target) comes
// from the InfraProvider — IaaS returns A/IPv4, managed-k8s would return
// CNAME/lb-hostname, and the DNSProvider picks its native representation
// from there.
//
// Orphan-domain cleanup: queries Caddy's live config (kc.GetCaddyRoutes)
// for currently-served domains and unroutes any that aren't in cfg.
// Caddy is the source of truth for live ingress (we don't keep state
// ourselves).
func RouteDomains(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, infra provider.InfraProvider, bctx *provider.BootstrapContext) error {
	if len(cfg.Domains) == 0 {
		return nil
	}
	dns, err := provider.ResolveDNS(dc.DNS.Name, dc.DNS.Creds)
	if err != nil {
		return fmt.Errorf("resolve dns provider: %w", err)
	}
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		binding, err := infra.IngressBinding(ctx, bctx, provider.ServiceTarget{
			Name: svcName,
			Port: cfg.Services[svcName].Port,
		})
		if err != nil {
			return fmt.Errorf("ingress binding for %s: %w", svcName, err)
		}
		dc.Cluster.Log().Command("dns", "set", svcName, "ip", binding.DNSTarget, "domains", cfg.Domains[svcName])
		for _, domain := range cfg.Domains[svcName] {
			dc.Cluster.Log().Progress(fmt.Sprintf("ensuring %s → %s", domain, binding.DNSTarget))
			if err := dns.RouteTo(ctx, domain, binding); err != nil {
				return fmt.Errorf("dns route %s: %w", domain, err)
			}
			dc.Cluster.Log().Success(domain)
		}
	}

	// Orphan-domain cleanup — query Caddy for live routes; any domain
	// not in cfg gets Unroute'd. Best-effort; Caddy may not be running
	// yet on first deploy (no orphans possible then anyway).
	desiredDomains := map[string]bool{}
	for _, svcDomains := range cfg.Domains {
		for _, d := range svcDomains {
			desiredDomains[d] = true
		}
	}
	if routes, err := dc.Cluster.MasterKube.GetCaddyRoutes(ctx); err == nil {
		for _, r := range routes {
			for _, domain := range r.Domains {
				if desiredDomains[domain] {
					continue
				}
				if err := dns.Unroute(ctx, domain); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan dns %s not removed: %s", domain, err))
				}
			}
		}
	}
	return nil
}

// masterServerType returns the server type string for the master node, used
// to derive the build platform via infra.ArchForType. Returns "" when no
// master is found (ValidateConfig would have caught that already).
func masterServerType(cfg *config.AppConfig) string {
	for _, srv := range cfg.Servers {
		if srv.Role == utils.RoleMaster {
			return srv.Type
		}
	}
	return ""
}
