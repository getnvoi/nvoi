package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

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
func Databases(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, sources map[string]string) (map[string]string, error) {
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	for _, name := range utils.SortedKeys(cfg.Databases) {
		def := cfg.Databases[name]

		// Block silent node-migration BEFORE any provider resolution or
		// cluster mutation. Selfhosted engines (postgres/mysql) pin data
		// to one node's local NVMe via k3s local-path — flipping
		// databases.X.server: would destroy the existing cluster's data
		// and initialize an empty PGDATA on the new node without warning.
		// SaaS engines (neon, planetscale) have no `server:` and skip
		// naturally (def.Server == ""). See #67 for the migrate command
		// that will lift this restriction.
		if err := guardNodePinDrift(ctx, dc, names, name, def); err != nil {
			return nil, err
		}

		creds, err := resolveDatabaseProviderCreds(dc.Creds, def.Engine)
		if err != nil {
			return nil, fmt.Errorf("databases.%s.provider: %w", name, err)
		}
		db, err := provider.ResolveDatabase(def.Engine, creds)
		if err != nil {
			return nil, fmt.Errorf("databases.%s: %w", name, err)
		}

		if err := db.ValidateCredentials(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("databases.%s: %w", name, err)
		}

		req, err := databaseRequest(dc, names, name, def, sources)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		req.Namespace = names.KubeNamespace()
		req.Labels = names.Labels()
		req.Log = dc.Cluster.Log()

		// Backup bucket + creds Secret — idempotent upsert. The bucket
		// is named deterministically (see Names.KubeDatabaseBackupBucket),
		// so re-running this step just re-asserts the retention policy
		// and re-materializes the creds Secret.
		if def.Backup != nil {
			if err := ensureDatabaseBackupBucket(ctx, dc, cfg, names, name, def, &req); err != nil {
				_ = db.Close()
				return nil, err
			}
		}

		resolved, err := db.EnsureCredentials(ctx, dc.Cluster.MasterKube, req)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("databases.%s.ensure credentials: %w", name, err)
		}
		plan, err := db.Reconcile(ctx, req)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("databases.%s.reconcile: %w", name, err)
		}
		for _, obj := range plan.Workloads {
			if err := dc.Cluster.MasterKube.Apply(ctx, names.KubeNamespace(), obj); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("databases.%s.apply: %w", name, err)
			}
		}
		out[utils.DatabaseEnvName(name)] = resolved.URL
		_ = db.Close()
	}

	return out, nil
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
		if err := dc.Cluster.MasterKube.EnsureSecret(
			ctx, names.KubeNamespace(), credsSecretName,
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
			Engine:   def.Engine,
			Version:  def.Version,
			Server:   def.Server,
			Size:     def.Size,
			Database: def.Database,
			Region:   def.Region,
		},
	}
	if def.Backup != nil {
		req.Spec.Backup = &provider.DatabaseBackupSpec{
			Schedule:  def.Backup.Schedule,
			Retention: def.Backup.Retention,
		}
	}
	if def.User != "" {
		v, err := resolveRef(def.User, sources)
		if err != nil {
			return req, fmt.Errorf("databases.%s.user: %w", name, err)
		}
		req.Spec.User = v
	}
	if def.Password != "" {
		v, err := resolveRef(def.Password, sources)
		if err != nil {
			return req, fmt.Errorf("databases.%s.password: %w", name, err)
		}
		req.Spec.Password = v
	}
	return req, nil
}

// guardNodePinDrift returns an error when a selfhosted database is
// already deployed on one node (per the live StatefulSet's
// nodeSelector[nvoi-role]) and cfg now asks for a different node. Local
// NVMe can't teleport — the new node would come up with empty PGDATA
// and the old data would be orphaned on the old node's disk. Fail
// loudly with a migration path instead.
//
// Skips when:
//   - def.Server is empty (SaaS engine — no node to pin to).
//   - No live StatefulSet exists (first deploy of this database).
//   - MasterKube is nil (defensive — shouldn't happen in a real reconcile).
//
// The check only reads cluster state; it does not mutate. Running
// Deploy twice with the same server: value is still idempotent.
func guardNodePinDrift(ctx context.Context, dc *config.DeployContext, names *utils.Names, name string, def config.DatabaseDef) error {
	if def.Server == "" {
		return nil
	}
	if dc.Cluster.MasterKube == nil {
		return nil
	}
	existing, err := dc.Cluster.MasterKube.GetStatefulSet(ctx, names.KubeNamespace(), names.Database(name))
	if err != nil {
		return fmt.Errorf("databases.%s: read live state: %w", name, err)
	}
	if existing == nil {
		return nil
	}
	current := existing.Spec.Template.Spec.NodeSelector[utils.LabelNvoiRole]
	if current == "" || current == def.Server {
		return nil
	}
	return fmt.Errorf(
		"databases.%s: server change blocked\n"+
			"  currently deployed on: %s\n"+
			"  nvoi.yaml says:        %s\n\n"+
			"Local NVMe cannot migrate between nodes. Automated migration via\n"+
			"`nvoi database migrate` is tracked in #67. Until it ships, migrate manually:\n\n"+
			"  1. nvoi database backup now %s\n"+
			"  2. remove databases.%s from nvoi.yaml, run: nvoi deploy\n"+
			"  3. restore config with server: %s, run: nvoi deploy\n"+
			"  4. restore the backup into the new instance (pg_restore / mysql)",
		name, current, def.Server, name, name, def.Server,
	)
}

func resolveDatabaseProviderCreds(source provider.CredentialSource, name string) (map[string]string, error) {
	schema, err := provider.GetSchema("database", name)
	if err != nil {
		return nil, err
	}
	return provider.ResolveFrom(schema, source)
}
