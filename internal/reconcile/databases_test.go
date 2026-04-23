package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	_ "github.com/getnvoi/nvoi/pkg/provider/postgres"
)

// TestDatabases_ProvisionsBackupBucketAndCredsSecret locks the unified
// backup contract end-to-end at the reconcile layer:
//
//   - `Databases()` resolves the per-database backup bucket name from
//     `Names.KubeDatabaseBackupBucket` (deterministic, derived from
//     Base() in one place).
//   - It EnsureBucket's that name on the configured providers.storage.
//   - It materializes the `-backup-creds` Secret with BUCKET_* + AWS_*
//     keys — the exact shape provider.BuildBackupCredsSecretData emits.
//   - The provider's Reconcile emits a CronJob; the reconciler applies
//     it through Cluster.MasterKube.
//
// Without these invariants the backup CronJob has no bucket to push to
// and no creds to sign with. Each assertion locks a specific step in
// the pipeline so a future change that breaks just one of them surfaces
// the right failure.
func TestDatabases_ProvisionsBackupBucketAndCredsSecret(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{})
	cf.RegisterBucket("test-bucket")

	dc := testDC(convergeMock())
	dc.Creds = testCreds()
	dc.Storage = app.ProviderRef{
		Name:  "test-bucket",
		Creds: map[string]string{"api_key": "x", "account_id": "acct"},
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute", Storage: "test-bucket"},
		Databases: map[string]config.DatabaseDef{
			"app": {
				Engine:   "postgres",
				Server:   "master",
				Size:     20,
				User:     "$POSTGRES_USER",
				Password: "$POSTGRES_PASSWORD",
				Database: "myapp",
				Backup:   &config.DatabaseBackupDef{Schedule: "0 3 * * *", Retention: 14},
			},
		},
	}

	sources := map[string]string{
		"POSTGRES_USER":     "appuser",
		"POSTGRES_PASSWORD": "s3cr3t",
	}

	out, err := Databases(context.Background(), dc, cfg, sources)
	if err != nil {
		t.Fatalf("Databases: %v", err)
	}

	// DATABASE_URL_APP appears in the merged sources map — proof that
	// consumer services downstream will envFrom the right DSN.
	if url, ok := out["DATABASE_URL_APP"]; !ok || url == "" {
		t.Fatalf("expected DATABASE_URL_APP in out, got %#v", out)
	}

	// Backup bucket was provisioned on the bucket provider — key check
	// for "enabling a database implicitly enables a storage bucket".
	bucketName := "nvoi-myapp-prod-db-app-backups"
	if !cf.HasBucket(bucketName) {
		t.Fatalf("expected bucket %q to be provisioned, have %#v", bucketName, cf.BucketNames())
	}

	// Retention policy was applied — the `set-lifecycle` op shows up in
	// the OpLog so retention drift (schedule changes) stays honest.
	if !cf.Has("set-lifecycle:" + bucketName) {
		t.Fatalf("expected set-lifecycle op for %s, have %v", bucketName, cf.All())
	}

	// -backup-creds Secret exists with the expected keys.
	kf := kfFor(dc)
	credsSecret := "nvoi-myapp-prod-db-app-backup-creds"
	if !kf.HasSecret("nvoi-myapp-prod", credsSecret) {
		t.Fatalf("expected Secret %s to exist", credsSecret)
	}
	for _, key := range []string{"BUCKET_ENDPOINT", "BUCKET_NAME", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION"} {
		v, err := kf.GetSecretValue(context.Background(), "nvoi-myapp-prod", credsSecret, key)
		if err != nil || v == "" {
			t.Fatalf("secret %s/%s missing or empty: %v", credsSecret, key, err)
		}
	}

	// The CronJob was applied through Cluster.MasterKube.
	if !kf.HasCronJob("nvoi-myapp-prod", "nvoi-myapp-prod-db-app-backup") {
		t.Fatalf("expected backup CronJob to be applied")
	}

	// The credentials Secret (the one engines write via EnsureCredentials)
	// is also present — it carries the `url` key the CronJob envFroms.
	if !kf.HasSecret("nvoi-myapp-prod", "nvoi-myapp-prod-db-app-credentials") {
		t.Fatalf("expected credentials Secret to exist")
	}
}

// TestDatabases_NoBackupSkipsBucketProvisioning locks the "only when
// requested" behavior: a database without `backup:` doesn't touch the
// bucket provider at all. Otherwise we'd leak implicit buckets for
// every declared database, defeating the opt-in contract.
func TestDatabases_NoBackupSkipsBucketProvisioning(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{})
	cf.RegisterBucket("test-bucket-noop")

	dc := testDC(convergeMock())
	dc.Creds = testCreds()
	dc.Storage = app.ProviderRef{
		Name:  "test-bucket-noop",
		Creds: map[string]string{"api_key": "x", "account_id": "acct"},
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute", Storage: "test-bucket-noop"},
		Databases: map[string]config.DatabaseDef{
			"app": {
				Engine:   "postgres",
				Server:   "master",
				Size:     20,
				User:     "$U",
				Password: "$P",
				Database: "myapp",
			},
		},
	}

	if _, err := Databases(context.Background(), dc, cfg, map[string]string{"U": "u", "P": "p"}); err != nil {
		t.Fatalf("Databases: %v", err)
	}

	bucketName := "nvoi-myapp-prod-db-app-backups"
	if cf.HasBucket(bucketName) {
		t.Fatalf("bucket %q must NOT exist when backup: unset", bucketName)
	}

	kf := kfFor(dc)
	if kf.HasSecret("nvoi-myapp-prod", "nvoi-myapp-prod-db-app-backup-creds") {
		t.Fatalf("backup-creds Secret must NOT exist when backup: unset")
	}
}
