// Package database implements the database package — a higher-level abstraction
// that bundles a PostgreSQL StatefulSet, headless Service, credentials,
// backup bucket, and backup CronJob from a simple config block.
package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/packages"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func init() {
	packages.Register(&DatabasePackage{})
}

// DatabasePackage implements packages.Package for PostgreSQL databases.
type DatabasePackage struct{}

func (d *DatabasePackage) Name() string { return "database" }

func (d *DatabasePackage) Active(cfg *config.AppConfig) bool {
	return len(cfg.Database) > 0
}

func (d *DatabasePackage) Validate(cfg *config.AppConfig) error {
	for name, db := range cfg.Database {
		if db.Image == "" {
			return fmt.Errorf("database.%s.image is required", name)
		}
		if db.Volume == "" {
			return fmt.Errorf("database.%s.volume is required", name)
		}
		if _, ok := cfg.Volumes[db.Volume]; !ok {
			return fmt.Errorf("database.%s.volume: %q is not a defined volume", name, db.Volume)
		}
		if cfg.Providers.Storage == "" {
			return fmt.Errorf("database.%s: providers.storage is required for database backups", name)
		}
		// Collision checks
		svcName := name + "-db"
		if _, ok := cfg.Services[svcName]; ok {
			return fmt.Errorf("database.%s: service %q conflicts — managed by database package", name, svcName)
		}
		cronName := name + "-db-backup"
		if _, ok := cfg.Crons[cronName]; ok {
			return fmt.Errorf("database.%s: cron %q conflicts — managed by database package", name, cronName)
		}
		storageName := name + "-db-backups"
		if _, ok := cfg.Storage[storageName]; ok {
			return fmt.Errorf("database.%s: storage %q conflicts — managed by database package", name, storageName)
		}
	}
	return nil
}

func (d *DatabasePackage) Reconcile(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) (map[string]string, error) {
	allEnvVars := map[string]string{}

	for _, name := range utils.SortedKeys(cfg.Database) {
		db := cfg.Database[name]
		envVars, err := reconcileDatabase(ctx, dc, cfg, name, db)
		if err != nil {
			return nil, fmt.Errorf("database.%s: %w", name, err)
		}
		for k, v := range envVars {
			allEnvVars[k] = v
		}
	}

	return allEnvVars, nil
}

func (d *DatabasePackage) Teardown(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, deleteStorage bool) error {
	if !deleteStorage {
		return nil
	}
	for _, name := range utils.SortedKeys(cfg.Database) {
		bucketName := name + "-db-backups"
		_ = app.StorageEmpty(ctx, app.StorageEmptyRequest{
			Cluster: app.Cluster{AppName: dc.Cluster.AppName, Env: dc.Cluster.Env, Output: dc.Cluster.Output},
			Storage: dc.Storage, Name: bucketName,
		})
		_ = app.StorageDelete(ctx, app.StorageDeleteRequest{
			Cluster: dc.Cluster, Storage: dc.Storage, Name: bucketName,
		})
	}
	return nil
}

func reconcileDatabase(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, name string, db config.DatabaseDef) (map[string]string, error) {
	out := dc.Cluster.Log()
	out.Command("database", "reconcile", name)

	engine, err := DetectEngine(db.Image)
	if err != nil {
		return nil, err
	}

	names, err := dc.Cluster.Names()
	if err != nil {
		return nil, err
	}

	creds, err := resolveCredentials(ctx, dc, name, engine)
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	svcName := name + "-db"
	ns := names.KubeNamespace()
	port := engine.Port()
	dbURL := engine.ConnectionURL(svcName, port, creds.User, creds.Password, creds.DBName)

	// Store credentials as k8s secret
	ssh, _, err := dc.Cluster.SSH(ctx)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	if err := storeCredentials(ctx, ssh, ns, name, engine, creds, dbURL); err != nil {
		return nil, err
	}
	out.Success("credentials stored")

	// Apply StatefulSet + headless Service
	vol := cfg.Volumes[db.Volume]
	serverName := vol.Server
	manifest := generateManifests(name, engine, db.Image, ns, names, serverName)
	if err := applyManifest(ctx, ssh, ns, manifest); err != nil {
		return nil, err
	}
	out.Success(fmt.Sprintf("%s applied", svcName))

	// Wait for postgres ready
	out.Progress(fmt.Sprintf("waiting for %s ready", svcName))
	if err := waitReady(ctx, ssh, ns, svcName, engine, creds.User); err != nil {
		return nil, err
	}
	out.Success(fmt.Sprintf("%s ready", svcName))

	// Backup bucket
	bucketName := name + "-db-backups"
	out.Progress(fmt.Sprintf("ensuring backup bucket %s", bucketName))
	if err := app.StorageSet(ctx, app.StorageSetRequest{
		Cluster: dc.Cluster, Storage: dc.Storage,
		Name: bucketName,
	}); err != nil {
		return nil, fmt.Errorf("backup bucket: %w", err)
	}
	out.Success(fmt.Sprintf("backup bucket %s", bucketName))

	// Backup CronJob
	schedule := db.Backup.Schedule
	if schedule == "" {
		schedule = "0 */6 * * *"
	}
	retain := db.Backup.Retain
	if retain == 0 {
		retain = 7
	}
	backupManifest := generateBackupCronJob(name, engine, db.Image, ns, names, svcName, schedule, retain, bucketName)
	if err := applyManifest(ctx, ssh, ns, backupManifest); err != nil {
		return nil, fmt.Errorf("backup cronjob: %w", err)
	}
	out.Success(fmt.Sprintf("backup cron (schedule: %s, retain: %d)", schedule, retain))

	// Build env vars for injection into app services
	prefix := strings.ToUpper(name)
	userEnv, passEnv, dbEnv := engine.EnvVarNames()
	envVars := map[string]string{
		prefix + "_DATABASE_URL": dbURL,
		prefix + "_" + userEnv:   creds.User,
		prefix + "_" + passEnv:   creds.Password,
		prefix + "_" + dbEnv:     creds.DBName,
		prefix + "_" + strings.ToUpper(engine.Name()) + "_HOST": svcName,
		prefix + "_" + strings.ToUpper(engine.Name()) + "_PORT": fmt.Sprintf("%d", port),
	}

	return envVars, nil
}
