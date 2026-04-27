package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/postgres"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// PendingMigration signals that a database has drift between its live
// StatefulSet's nodeSelector and cfg.databases.X.server. Threaded up
// from Databases() to reconcile.Deploy, which prints an end-of-deploy
// summary listing pending migrations. Deploy always exits 0; operators
// resolve drift explicitly via `nvoi database migrate <name>`.
//
// The shape is intentionally narrow — just enough to render the
// summary. Full orchestration lives in cmd/cli/database.go.
type PendingMigration struct {
	Database string // the key in cfg.Databases (e.g. "main")
	From     string // current node (live StatefulSet's nodeSelector value)
	To       string // target node (cfg.databases.X.server)
}

// Databases converges every entry in `cfg.Databases` against the
// configured provider. Step runs between Storage() and Services() so
// consumer services can envFrom `DATABASE_URL_<NAME>` out of the merged
// credential map this step returns.
//
// When `def.Backup` is set, this step also provisions the per-database
// backup bucket on `providers.storage` (one bucket per database,
// prefix-free), applies the retention policy, and materializes the
// `-backup-creds` cluster Secret that the uniform backup CronJob
// envFroms. The provider's Reconcile then emits the CronJob itself.
//
// Returns the per-DB URLs AND a list of databases whose live node
// differs from cfg — those get their StatefulSet reapply SKIPPED
// (old pod keeps serving, DATABASE_URL stays stable because the k8s
// Service name is pod-agnostic) and the caller surfaces a warning
// summary. Deploy never fails on drift; the destructive act of moving
// data lives in the explicit `nvoi database migrate` verb (#67).
func Databases(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, sources map[string]string) (map[string]string, []PendingMigration, error) {
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, nil, err
	}

	// Install the OpenEBS ZFS-LocalPV CSI driver once, up front, when
	// any selfhosted database is declared. Idempotent — kubectl apply
	// patches what changed, creates what's new, no-ops the rest. Runs
	// via the master's SSH (kubectl bundled with k3s) so we don't have
	// to plumb the CSI's ~400-line manifest through our typed applier.
	// Skipped when no selfhosted DBs exist (SaaS-only configs never
	// need the CSI).
	if hasSelfhostedDatabase(cfg) && dc.Cluster.NodeShell != nil {
		if err := postgres.EnsureZFSCSI(ctx, dc.Cluster.NodeShell); err != nil {
			return nil, nil, fmt.Errorf("ensure zfs-localpv csi: %w", err)
		}
	}

	// TODO(#68, ttl): sweep orphan branches. Walk all workloads labeled
	// `nvoi/branch-of=<src-pvc>` + `nvoi/branch-ttl=<RFC3339>`, compare
	// the expiry against time.Now(), and call postgres.DeleteBranch for
	// every expired entry. Runs here (once per deploy) rather than a
	// sidecar cronjob so the cleanup lifecycle tracks nvoi's own
	// reconcile cadence — no extra k8s controller to ship. Resurface
	// when TTL labels start being emitted (paired TODO in
	// pkg/provider/postgres/branching.go::Branch).

	out := map[string]string{}
	var pending []PendingMigration

	for _, name := range utils.SortedKeys(cfg.Databases) {
		def := cfg.Databases[name]

		// Detect node-pin drift BEFORE any provider resolution or cluster
		// mutation. Selfhosted engines pin data to one node's local NVMe
		// (or ZFS dataset per #68); flipping databases.X.server: can't be
		// converged by `nvoi deploy` without destroying the existing
		// cluster's data. Instead of failing the deploy, we emit a
		// warning, keep the old pod serving, and let the operator resolve
		// via `nvoi database migrate`. SaaS engines (neon, planetscale)
		// have no `server:` and skip naturally (def.Server == "").
		drift, err := detectNodePinDrift(ctx, dc, names, name, def)
		if err != nil {
			return nil, nil, err
		}
		if drift != nil {
			pending = append(pending, *drift)
			// Pull the DB URL from the existing credentials Secret —
			// DATABASE_URL_X is derived from the k8s Service name, which
			// is pod-agnostic and stays stable across the pending-
			// migration window. Consumer services connect to the old
			// pod on the old node until migrate runs.
			url, err := readExistingDatabaseURL(ctx, dc, names, name)
			if err != nil {
				return nil, nil, fmt.Errorf("databases.%s: read existing URL: %w", name, err)
			}
			out[utils.DatabaseEnvName(name)] = url
			if dc.Cluster.Log() != nil {
				dc.Cluster.Log().Warning(fmt.Sprintf(
					"databases.%s: node change pending — still serving from %s. Run: nvoi database migrate %s",
					name, drift.From, name,
				))
			}
			continue
		}

		url, err := ReconcileOneDatabase(ctx, dc, cfg, names, name, def, sources)
		if err != nil {
			return nil, nil, err
		}
		out[utils.DatabaseEnvName(name)] = url
	}

	// Orphan sweep — owner=databases is scoped per the discriminator,
	// so this can never see services / crons / tunnel / caddy / registries
	// resources. Branches carry owner=database-branches and survive
	// (their lifecycle is managed by `nvoi database branch ...`).
	//
	// Removing `databases.X` from cfg drops every per-DB workload from
	// the desired sets below, so SweepOwned deletes them on the next
	// reconcile. Removing only `databases.X.backup` drops the backup
	// CronJob + backup-creds Secret without touching the StatefulSet.
	desiredStatefulSets := []string{}
	desiredServices := []string{}
	desiredPVCs := []string{}
	desiredSecrets := []string{}
	desiredCronJobs := []string{}
	for _, name := range utils.SortedKeys(cfg.Databases) {
		def := cfg.Databases[name]
		// SaaS engines (no Server set) emit no StatefulSet/Service/PVC
		// — only the credentials Secret + (optional) backup CronJob +
		// (optional) backup-creds Secret. Selfhosted engines emit all
		// of the above.
		if def.Server != "" {
			desiredStatefulSets = append(desiredStatefulSets, names.Database(name))
			desiredServices = append(desiredServices, names.Database(name))
			desiredPVCs = append(desiredPVCs, names.KubeDatabasePVC(name))
		}
		desiredSecrets = append(desiredSecrets, names.KubeDatabaseCredentials(name))
		if def.Backup != nil {
			desiredCronJobs = append(desiredCronJobs, names.KubeDatabaseBackupCron(name))
			desiredSecrets = append(desiredSecrets, names.KubeDatabaseBackupCreds(name))
		}
	}
	ns := names.KubeNamespace()
	kc := dc.Cluster.MasterKube
	if kc != nil {
		for _, sweep := range []struct {
			kind    kube.Kind
			desired []string
		}{
			{kube.KindStatefulSet, desiredStatefulSets},
			{kube.KindService, desiredServices},
			{kube.KindPVC, desiredPVCs},
			{kube.KindSecret, desiredSecrets},
			{kube.KindCronJob, desiredCronJobs},
		} {
			if err := provider.SweepOwned(ctx, kc, ns, provider.KindDatabase, sweep.kind, sweep.desired); err != nil {
				if log := dc.Cluster.Log(); log != nil {
					log.Warning(fmt.Sprintf("databases sweep %s: %s", sweep.kind, err))
				}
			}
		}
	}

	return out, pending, nil
}

// ReconcileOneDatabase applies a single database's provider-owned
// workloads (StatefulSet + Service + PVC + backup CronJob for
// selfhosted engines; backup CronJob only for SaaS) and returns the
// DATABASE_URL for it. Extracted from Databases() so `nvoi database
// migrate` can reuse the exact same apply path — the migrate command
// tears down the old workloads and then calls this helper to rebuild
// per cfg, with zero duplication.
//
// No drift guard here — the caller is responsible for deciding whether
// to run this (first deploy, cfg/live converged, or post-teardown in
// migrate). Calling it on a live DB that differs from cfg.server would
// recreate the StatefulSet on the new node with fresh PGDATA — which
// is exactly what migrate wants after it has captured a backup.
func ReconcileOneDatabase(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, names *utils.Names, name string, def config.DatabaseDef, sources map[string]string) (string, error) {
	out := dc.Cluster.Log()

	// Open the operator-facing group with engine + topology so the
	// deploy log shows what's about to converge per database. Mirrors
	// how `service set api`, `instance set ...`, `firewall set ...`
	// open their groups — the sibling reconcile steps are loud, this
	// one used to be silent which made it look like database wasn't
	// reconciling at all.
	if def.Server != "" {
		out.Command("database", "set", name,
			"engine", def.Engine,
			"server", def.Server,
			"size", fmt.Sprintf("%dGiB", def.Size),
		)
	} else if def.Region != "" {
		out.Command("database", "set", name,
			"engine", def.Engine,
			"region", def.Region,
		)
	} else {
		out.Command("database", "set", name, "engine", def.Engine)
	}

	creds, err := resolveDatabaseProviderCreds(dc.Creds, def.Engine)
	if err != nil {
		return "", fmt.Errorf("databases.%s.provider: %w", name, err)
	}
	db, err := provider.ResolveDatabase(def.Engine, creds)
	if err != nil {
		return "", fmt.Errorf("databases.%s: %w", name, err)
	}
	defer db.Close()

	if err := db.ValidateCredentials(ctx); err != nil {
		return "", fmt.Errorf("databases.%s: %w", name, err)
	}

	// Resolve the backup-image digest once per deploy, before any Job
	// gets stamped with it. Pinning by digest is what forces kubelet to
	// repull when bin/deploy pushes a new build of cmd/db — the tag
	// alone (`:latest`) never invalidates the kubelet cache, so a
	// single bad push silently jams every backup pod into
	// ImagePullBackOff. Hard-fails the deploy if the registry doesn't
	// resolve the tag (image was never pushed, registry down, etc.) —
	// fail at deploy time, not at 3am when cron fires.
	imageRef, err := provider.ResolveDBImage(ctx)
	if err != nil {
		return "", fmt.Errorf("databases.%s: %w", name, err)
	}

	req, err := databaseRequest(dc, names, name, def, sources)
	if err != nil {
		return "", err
	}
	req.DBImageRef = imageRef
	req.Namespace = names.KubeNamespace()
	// KubeLabels (not Labels) — DB workloads land in k8s, not on the
	// IaaS provider. The kube-side label needs the
	// `app.kubernetes.io/managed-by` prefix so kube.NvoiSelector
	// matches; bare `managed-by` (Labels()) is for provider-side
	// resources only.
	req.Labels = names.KubeLabels()
	req.Log = out
	req.Kube = dc.Cluster.MasterKube

	// Dial SSH to the DB's target node for any host-level setup the
	// provider needs (postgres: ZFS prepare-node). Skipped for SaaS
	// engines (def.Server == "") and when the cluster doesn't expose a
	// per-node shell. The provider decides whether to use req.NodeSSH
	// or ignore it.
	if def.Server != "" {
		ns, err := dc.Cluster.SSHToNode(ctx, config.NewView(cfg), def.Server)
		if err == nil && ns != nil {
			req.NodeSSH = ns
			defer ns.Close()
		}
		// Non-fatal: if SSH to the target node fails, the provider's
		// Reconcile falls back to skipping any host setup. The
		// StatefulSet apply will surface a concrete error (e.g. PVC
		// stuck Pending) if the skipped setup was actually required.
	}

	if def.Backup != nil {
		if err := ensureDatabaseBackupBucket(ctx, dc, cfg, names, name, def, &req); err != nil {
			return "", err
		}
		out.Success(fmt.Sprintf(
			"backup bucket %s (schedule: %s, retention: %dd)",
			names.KubeDatabaseBackupBucket(name), def.Backup.Schedule, def.Backup.Retention,
		))
	}

	resolved, err := db.EnsureCredentials(ctx, dc.Cluster.MasterKube, req)
	if err != nil {
		return "", fmt.Errorf("databases.%s.ensure credentials: %w", name, err)
	}
	out.Success(fmt.Sprintf("credentials secret %s applied", names.KubeDatabaseCredentials(name)))

	plan, err := db.Reconcile(ctx, req)
	if err != nil {
		return "", fmt.Errorf("databases.%s.reconcile: %w", name, err)
	}
	for _, obj := range plan.Workloads {
		if err := provider.ApplyOwned(ctx, dc.Cluster.MasterKube, names.KubeNamespace(), provider.KindDatabase, obj); err != nil {
			return "", fmt.Errorf("databases.%s.apply: %w", name, err)
		}
	}
	out.Success("applied")

	return resolved.URL, nil
}

// readExistingDatabaseURL fetches the `url` key from an existing
// credentials Secret. Used by the drift path so DATABASE_URL_X stays
// populated in the sources map while the DB's node change is pending —
// consumer services read the value, connect to the in-cluster Service
// name (unchanged), and continue working against the old pod until
// migrate runs.
func readExistingDatabaseURL(ctx context.Context, dc *config.DeployContext, names *utils.Names, name string) (string, error) {
	if dc.Cluster.MasterKube == nil {
		return "", fmt.Errorf("kube client required to read existing credentials Secret")
	}
	return dc.Cluster.MasterKube.GetSecretValue(
		ctx,
		names.KubeNamespace(),
		names.KubeDatabaseCredentials(name),
		"url",
	)
}

// ensureDatabaseBackupBucket provisions the per-database backup bucket
// on the configured `providers.storage`, applies the retention
// lifecycle, and writes the cluster-side Secret the backup CronJob /
// one-shot Job envFroms. Mutates req so the provider's Reconcile knows
// where to point the CronJob.
//
// Validator guarantees `cfg.Providers.Storage != ""` when def.Backup is
// set, so the error path here is a provider-side failure, not a config
// issue.
func ensureDatabaseBackupBucket(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, names *utils.Names, dbName string, def config.DatabaseDef, req *provider.DatabaseRequest) error {
	_ = cfg
	if dc.Storage.Name == "" {
		return fmt.Errorf("databases.%s.backup: providers.storage is not configured (validator should have caught this)", dbName)
	}
	bucket, err := provider.ResolveBucket(dc.Storage.Name, dc.Storage.Creds)
	if err != nil {
		return fmt.Errorf("databases.%s.backup: resolve bucket provider: %w", dbName, err)
	}
	bucketName := names.KubeDatabaseBackupBucket(dbName)
	if err := bucket.EnsureBucket(ctx, bucketName); err != nil {
		return fmt.Errorf("databases.%s.backup: ensure bucket %s: %w", dbName, bucketName, err)
	}
	if def.Backup.Retention > 0 {
		if err := bucket.SetLifecycle(ctx, bucketName, def.Backup.Retention); err != nil {
			return fmt.Errorf("databases.%s.backup: set lifecycle: %w", dbName, err)
		}
	}
	bucketCreds, err := bucket.Credentials(ctx)
	if err != nil {
		return fmt.Errorf("databases.%s.backup: fetch bucket credentials: %w", dbName, err)
	}
	// Cluster-side Secret — the CronJob / one-shot Job envFroms this to
	// get BUCKET_ENDPOINT / BUCKET_NAME / AWS_* for the sigv4 upload.
	// Shape owned by provider.BuildBackupCredsSecretData so the image's
	// entrypoint contract stays in one place.
	credsSecretName := names.KubeDatabaseBackupCreds(dbName)
	if dc.Cluster.MasterKube != nil {
		if err := provider.EnsureSecret(
			ctx, dc.Cluster.MasterKube, names.KubeNamespace(),
			provider.KindDatabase, credsSecretName,
			provider.BuildBackupCredsSecretData(bucketName, bucketCreds),
		); err != nil {
			return fmt.Errorf("databases.%s.backup: write %s: %w", dbName, credsSecretName, err)
		}
	}
	req.Bucket = &provider.BucketHandle{Name: bucketName, Credentials: bucketCreds}
	req.BackupCredsSecretName = credsSecretName
	return nil
}

func databaseRequest(dc *config.DeployContext, names *utils.Names, name string, def config.DatabaseDef, sources map[string]string) (provider.DatabaseRequest, error) {
	_ = dc
	req := provider.DatabaseRequest{
		Name:                  name,
		FullName:              names.Database(name),
		PodName:               names.KubeDatabasePod(name),
		PVCName:               names.KubeDatabasePVC(name),
		BackupName:            names.KubeDatabaseBackupCron(name),
		CredentialsSecretName: names.KubeDatabaseCredentials(name),
		Spec: provider.DatabaseSpec{
			Engine:  def.Engine,
			Version: def.Version,
			Server:  def.Server,
			Size:    def.Size,
			Region:  def.Region,
		},
	}
	if def.Backup != nil {
		req.Spec.Backup = &provider.DatabaseBackupSpec{
			Schedule:  def.Backup.Schedule,
			Retention: def.Backup.Retention,
		}
	}
	// credentials.user / .password / .database all support $VAR references
	// resolved against the same source map the services/crons pipeline
	// uses. Keeping the three fields in lockstep avoids the foot-gun where
	// $MAIN_POSTGRES_USER resolves but $MAIN_POSTGRES_DB gets passed
	// through literally and postgres rejects the DSN at connect time.
	// SaaS engines have no credentials block (validator rejects it) — the
	// nil check here is what skips this path for them.
	if def.Credentials != nil {
		if def.Credentials.User != "" {
			v, err := resolveRef(def.Credentials.User, sources)
			if err != nil {
				return req, fmt.Errorf("databases.%s.credentials.user: %w", name, err)
			}
			req.Spec.User = v
		}
		if def.Credentials.Password != "" {
			v, err := resolveRef(def.Credentials.Password, sources)
			if err != nil {
				return req, fmt.Errorf("databases.%s.credentials.password: %w", name, err)
			}
			req.Spec.Password = v
		}
		if def.Credentials.Database != "" {
			v, err := resolveRef(def.Credentials.Database, sources)
			if err != nil {
				return req, fmt.Errorf("databases.%s.credentials.database: %w", name, err)
			}
			req.Spec.Database = v
		}
	}
	return req, nil
}

// detectNodePinDrift returns a PendingMigration when a selfhosted
// database is already deployed on one node (per the live StatefulSet's
// nodeSelector[nvoi-role]) and cfg now asks for a different node.
// Returns (nil, nil) for the happy cases: no `server:` (SaaS), no live
// StatefulSet (first deploy), live node matches cfg, or no kube client.
//
// The check only reads cluster state; it never mutates. Running Deploy
// twice with the same server: value is still idempotent — live matches
// cfg, no drift, the normal reconcile path runs.
func detectNodePinDrift(ctx context.Context, dc *config.DeployContext, names *utils.Names, name string, def config.DatabaseDef) (*PendingMigration, error) {
	if def.Server == "" {
		return nil, nil
	}
	if dc.Cluster.MasterKube == nil {
		return nil, nil
	}
	existing, err := dc.Cluster.MasterKube.GetStatefulSet(ctx, names.KubeNamespace(), names.Database(name))
	if err != nil {
		return nil, fmt.Errorf("databases.%s: read live state: %w", name, err)
	}
	if existing == nil {
		return nil, nil
	}
	current := existing.Spec.Template.Spec.NodeSelector[utils.LabelNvoiRole]
	if current == "" || current == def.Server {
		return nil, nil
	}
	return &PendingMigration{
		Database: name,
		From:     current,
		To:       def.Server,
	}, nil
}

// hasSelfhostedDatabase reports whether any DB in cfg uses a
// selfhosted engine (postgres, mysql). SaaS engines (neon,
// planetscale) don't need the CSI driver installed cluster-side since
// they have no in-cluster storage.
func hasSelfhostedDatabase(cfg *config.AppConfig) bool {
	for _, db := range cfg.Databases {
		switch db.Engine {
		case "postgres", "mysql":
			return true
		}
	}
	return false
}

func resolveDatabaseProviderCreds(source provider.CredentialSource, name string) (map[string]string, error) {
	schema, err := provider.GetSchema("database", name)
	if err != nil {
		return nil, err
	}
	return provider.ResolveFrom(schema, source)
}
