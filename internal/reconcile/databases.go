package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Databases(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, sources map[string]string) (map[string]string, error) {
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	for _, name := range utils.SortedKeys(cfg.Databases) {
		def := cfg.Databases[name]

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

		req, err := databaseRequest(cfg, dc, names, name, def, sources)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		req.Namespace = names.KubeNamespace()
		req.Labels = names.Labels()
		req.Log = dc.Cluster.Log()

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

func databaseRequest(cfg *config.AppConfig, dc *config.DeployContext, names *utils.Names, name string, def config.DatabaseDef, sources map[string]string) (provider.DatabaseRequest, error) {
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
			Storage:   def.Backup.Storage,
		}
		if def.Backup.Storage != "" {
			bucketCreds, err := databaseBucketCredentials(cfg, dc, def.Backup.Storage)
			if err != nil {
				return req, fmt.Errorf("databases.%s.bucket: %w", name, err)
			}
			req.Bucket = &provider.BucketHandle{
				Name:        names.Bucket(def.Backup.Storage),
				Credentials: bucketCreds,
			}
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

func databaseBucketCredentials(cfg *config.AppConfig, dc *config.DeployContext, storageName string) (provider.BucketCredentials, error) {
	_ = cfg
	_ = storageName
	if dc.Storage.Name == "" {
		return provider.BucketCredentials{}, fmt.Errorf("providers.storage is required for database backups")
	}
	bucket, err := provider.ResolveBucket(dc.Storage.Name, dc.Storage.Creds)
	if err != nil {
		return provider.BucketCredentials{}, err
	}
	return bucket.Credentials(context.Background())
}

func resolveDatabaseProviderCreds(source provider.CredentialSource, name string) (map[string]string, error) {
	schema, err := provider.GetSchema("database", name)
	if err != nil {
		return nil, err
	}
	return provider.ResolveFrom(schema, source)
}
