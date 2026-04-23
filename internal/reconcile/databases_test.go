package reconcile

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	_ "github.com/getnvoi/nvoi/pkg/provider/postgres"
	"github.com/getnvoi/nvoi/pkg/utils"
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

// TestDatabases_ResolvesVarRefsInAllCredFields locks the invariant
// that user / password / database all resolve $VAR references against
// the same source map. Previously only user + password did — database
// passed through literally, so `$MAIN_POSTGRES_DB` would hit postgres
// as the literal string "$MAIN_POSTGRES_DB" and connect would fail.
// The three fields have to stay in lockstep.
func TestDatabases_ResolvesVarRefsInAllCredFields(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{})
	cf.RegisterBucket("test-bucket-vars")

	dc := testDC(convergeMock())
	dc.Creds = testCreds()
	dc.Storage = app.ProviderRef{
		Name:  "test-bucket-vars",
		Creds: map[string]string{"api_key": "x", "account_id": "acct"},
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute", Storage: "test-bucket-vars"},
		Databases: map[string]config.DatabaseDef{
			"app": {
				Engine:   "postgres",
				Server:   "master",
				Size:     20,
				User:     "$PG_USER",
				Password: "$PG_PASS",
				Database: "$PG_DB",
			},
		},
	}

	sources := map[string]string{
		"PG_USER": "resolveduser",
		"PG_PASS": "resolvedpass",
		"PG_DB":   "resolveddb",
	}

	out, err := Databases(context.Background(), dc, cfg, sources)
	if err != nil {
		t.Fatalf("Databases: %v", err)
	}

	// The synthesized DATABASE_URL_APP is built from all three resolved
	// fields. Presence of each resolved value there proves none of the
	// fields short-circuited with a literal "$VAR".
	url := out["DATABASE_URL_APP"]
	for _, want := range []string{"resolveduser", "resolvedpass", "resolveddb"} {
		if !strings.Contains(url, want) {
			t.Errorf("DATABASE_URL_APP missing %q; got %q", want, url)
		}
	}
	if strings.Contains(url, "$") {
		t.Errorf("DATABASE_URL_APP contains unresolved $: %q", url)
	}

	// The in-cluster credentials Secret holds the resolved database
	// name too — the postgres entrypoint reads this key directly.
	kf := kfFor(dc)
	got, err := kf.GetSecretValue(context.Background(), "nvoi-myapp-prod",
		"nvoi-myapp-prod-db-app-credentials", "database")
	if err != nil {
		t.Fatalf("read credentials Secret: %v", err)
	}
	if got != "resolveddb" {
		t.Errorf("credentials Secret database = %q, want %q", got, "resolveddb")
	}
}

// TestDatabases_UnresolvedVarInDatabaseErrors locks the error path:
// a $VAR reference in database: that can't be resolved must fail with
// a field-qualified error, matching the existing user/password shape.
func TestDatabases_UnresolvedVarInDatabaseErrors(t *testing.T) {
	dc := testDC(convergeMock())
	dc.Creds = testCreds()

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute"},
		Databases: map[string]config.DatabaseDef{
			"app": {
				Engine:   "postgres",
				Server:   "master",
				Size:     20,
				User:     "u",
				Password: "p",
				Database: "$MISSING",
			},
		},
	}

	_, err := Databases(context.Background(), dc, cfg, map[string]string{})
	if err == nil {
		t.Fatal("expected error for unresolved $MISSING in database:, got nil")
	}
	if !strings.Contains(err.Error(), "databases.app.database") {
		t.Errorf("error should be field-qualified; got: %s", err.Error())
	}
}

// TestDatabases_HardErrorOnServerChange locks the "local NVMe cannot
// migrate" invariant. Selfhosted databases pin their data to one node
// via k3s local-path — flipping databases.X.server: would silently
// initialize an empty PGDATA on the new node and orphan the old data.
//
// The guard runs BEFORE any provider resolution or kube mutation, so a
// blocked deploy leaves the cluster untouched. We assert three things:
//
//  1. Deploy errors naming both the current and requested nodes so the
//     operator can see the drift without digging through kubectl.
//  2. The error message points at the migration path (issue #67) so
//     this isn't a dead-end — there IS a documented escape hatch.
//  3. Zero cluster mutations happened for this database — no CronJob,
//     no credentials Secret, no bucket side-effects. The guard is
//     purely a read, and it short-circuits everything downstream.
func TestDatabases_HardErrorOnServerChange(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{})
	cf.RegisterBucket("test-bucket-guard")

	dc := testDC(convergeMock())
	dc.Creds = testCreds()
	dc.Storage = app.ProviderRef{
		Name:  "test-bucket-guard",
		Creds: map[string]string{"api_key": "x", "account_id": "acct"},
	}

	// Seed a live StatefulSet pinned to "master" — this is the state
	// that would exist after a prior successful deploy.
	kf := kfFor(dc)
	existing := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nvoi-myapp-prod-db-app",
			Namespace: "nvoi-myapp-prod",
		},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{utils.LabelNvoiRole: "master"},
				},
			},
		},
	}
	if _, err := kf.Typed.AppsV1().StatefulSets("nvoi-myapp-prod").Create(
		context.Background(), existing, metav1.CreateOptions{},
	); err != nil {
		t.Fatalf("seed existing StatefulSet: %v", err)
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute", Storage: "test-bucket-guard"},
		Databases: map[string]config.DatabaseDef{
			"app": {
				Engine:   "postgres",
				Server:   "db-master", // changed from "master"
				Size:     20,
				User:     "$U",
				Password: "$P",
				Database: "myapp",
				Backup:   &config.DatabaseBackupDef{Schedule: "0 3 * * *", Retention: 14},
			},
		},
	}

	_, err := Databases(context.Background(), dc, cfg,
		map[string]string{"U": "u", "P": "p"})
	if err == nil {
		t.Fatal("expected hard error on server change, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"master", "db-master", "#67", "databases.app"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}

	// Zero mutations past the guard — the backup bucket must NOT have
	// been provisioned, the credentials Secret must NOT have been
	// written, and no CronJob was applied. A future regression that
	// reorders the guard below any provider op would flip these.
	if cf.HasBucket("nvoi-myapp-prod-db-app-backups") {
		t.Error("backup bucket must not be provisioned when guard trips")
	}
	if kf.HasSecret("nvoi-myapp-prod", "nvoi-myapp-prod-db-app-backup-creds") {
		t.Error("backup-creds Secret must not be written when guard trips")
	}
	if kf.HasSecret("nvoi-myapp-prod", "nvoi-myapp-prod-db-app-credentials") {
		t.Error("credentials Secret must not be written when guard trips")
	}
	if kf.HasCronJob("nvoi-myapp-prod", "nvoi-myapp-prod-db-app-backup") {
		t.Error("backup CronJob must not be applied when guard trips")
	}
}

// TestDatabases_FirstDeploySkipsGuard locks the "guard is drift-only,
// not first-deploy" property. When no live StatefulSet exists yet, the
// guard must return nil and let reconcile proceed — otherwise no
// database could ever be deployed for the first time.
func TestDatabases_FirstDeploySkipsGuard(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{})
	cf.RegisterBucket("test-bucket-first")

	dc := testDC(convergeMock())
	dc.Creds = testCreds()
	dc.Storage = app.ProviderRef{
		Name:  "test-bucket-first",
		Creds: map[string]string{"api_key": "x", "account_id": "acct"},
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute", Storage: "test-bucket-first"},
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

	if _, err := Databases(context.Background(), dc, cfg,
		map[string]string{"U": "u", "P": "p"}); err != nil {
		t.Fatalf("first-deploy must succeed (no live StatefulSet), got: %v", err)
	}
}
