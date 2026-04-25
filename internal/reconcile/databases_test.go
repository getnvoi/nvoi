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
				Engine: "postgres",
				Server: "master",
				Size:   20,
				Credentials: &config.DatabaseCredentialsDef{
					User:     "$POSTGRES_USER",
					Password: "$POSTGRES_PASSWORD",
					Database: "myapp",
				},
				Backup: &config.DatabaseBackupDef{Schedule: "0 3 * * *", Retention: 14},
			},
		},
	}

	sources := map[string]string{
		"POSTGRES_USER":     "appuser",
		"POSTGRES_PASSWORD": "s3cr3t",
	}

	out, pending, err := Databases(context.Background(), dc, cfg, sources)
	if err != nil {
		t.Fatalf("Databases: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("no pending migrations expected on fresh deploy, got %+v", pending)
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
				Engine: "postgres",
				Server: "master",
				Size:   20,
				Credentials: &config.DatabaseCredentialsDef{
					User:     "$U",
					Password: "$P",
					Database: "myapp",
				},
			},
		},
	}

	if _, _, err := Databases(context.Background(), dc, cfg, map[string]string{"U": "u", "P": "p"}); err != nil {
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
				Engine: "postgres",
				Server: "master",
				Size:   20,
				Credentials: &config.DatabaseCredentialsDef{
					User:     "$PG_USER",
					Password: "$PG_PASS",
					Database: "$PG_DB",
				},
			},
		},
	}

	sources := map[string]string{
		"PG_USER": "resolveduser",
		"PG_PASS": "resolvedpass",
		"PG_DB":   "resolveddb",
	}

	out, _, err := Databases(context.Background(), dc, cfg, sources)
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
				Engine: "postgres",
				Server: "master",
				Size:   20,
				Credentials: &config.DatabaseCredentialsDef{
					User:     "u",
					Password: "p",
					Database: "$MISSING",
				},
			},
		},
	}

	_, _, err := Databases(context.Background(), dc, cfg, map[string]string{})
	if err == nil {
		t.Fatal("expected error for unresolved $MISSING in credentials.database, got nil")
	}
	if !strings.Contains(err.Error(), "databases.app.credentials.database") {
		t.Errorf("error should be field-qualified; got: %s", err.Error())
	}
}

// TestDatabases_NodeDriftEmitsPendingMigration locks the warning-not-
// failure contract (#67): when a live StatefulSet's nodeSelector
// differs from cfg.server, Databases() must:
//
//  1. Return success (no error) — deploy must not fail on drift.
//  2. Emit a PendingMigration for the drifting DB.
//  3. Preserve DATABASE_URL_X in the sources map by reading from the
//     existing credentials Secret, so consumer services stay connected
//     to the old pod while migrate is pending.
//  4. NOT re-apply the StatefulSet on the new node (would fight the
//     live workload; destructive work lives in `nvoi database migrate`).
//  5. NOT provision backup infra for this DB — the guard short-circuits
//     everything downstream, leaving the live resources untouched.
//
// This is the architectural opposite of a hard-error: a typo in
// nvoi.yaml must not trigger silent data movement. The operator resolves
// drift by running `nvoi database migrate` explicitly.
func TestDatabases_NodeDriftEmitsPendingMigration(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{})
	cf.RegisterBucket("test-bucket-drift")

	dc := testDC(convergeMock())
	dc.Creds = testCreds()
	dc.Storage = app.ProviderRef{
		Name:  "test-bucket-drift",
		Creds: map[string]string{"api_key": "x", "account_id": "acct"},
	}

	kf := kfFor(dc)

	// Seed the post-prior-deploy state: a live StatefulSet pinned to
	// "master" plus its credentials Secret (containing the URL
	// consumers have cached).
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
	if err := kf.Client.EnsureSecret(
		context.Background(), "nvoi-myapp-prod", "nvoi-myapp-prod-db-app-credentials",
		map[string]string{"url": "postgres://live@master:5432/myapp"},
	); err != nil {
		t.Fatalf("seed credentials Secret: %v", err)
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute", Storage: "test-bucket-drift"},
		Databases: map[string]config.DatabaseDef{
			"app": {
				Engine: "postgres",
				Server: "db-master", // changed from "master"
				Size:   20,
				Credentials: &config.DatabaseCredentialsDef{
					User:     "$U",
					Password: "$P",
					Database: "myapp",
				},
				Backup: &config.DatabaseBackupDef{Schedule: "0 3 * * *", Retention: 14},
			},
		},
	}

	out, pending, err := Databases(context.Background(), dc, cfg,
		map[string]string{"U": "u", "P": "p"})
	if err != nil {
		t.Fatalf("Databases must NOT error on drift (warning-not-failure contract); got: %v", err)
	}

	// 1. Pending migration is surfaced exactly once, with both nodes
	// named so the end-of-deploy summary is useful.
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending migration, got %d: %+v", len(pending), pending)
	}
	if pending[0].Database != "app" || pending[0].From != "master" || pending[0].To != "db-master" {
		t.Errorf("pending migration wrong shape: got %+v", pending[0])
	}

	// 2. URL is preserved from the existing credentials Secret —
	// consumer services keep working throughout the pending window.
	if got := out["DATABASE_URL_APP"]; got != "postgres://live@master:5432/myapp" {
		t.Errorf("DATABASE_URL_APP should mirror existing Secret, got %q", got)
	}

	// 3. Bucket must NOT have been provisioned — the drift short-
	// circuit skips the full per-DB reconcile, including backup setup.
	if cf.HasBucket("nvoi-myapp-prod-db-app-backups") {
		t.Error("backup bucket must not be provisioned on drift")
	}
	if kf.HasSecret("nvoi-myapp-prod", "nvoi-myapp-prod-db-app-backup-creds") {
		t.Error("backup-creds Secret must not be written on drift")
	}
	if kf.HasCronJob("nvoi-myapp-prod", "nvoi-myapp-prod-db-app-backup") {
		t.Error("backup CronJob must not be applied on drift")
	}
}

// TestDatabases_FirstDeploySkipsDriftDetection locks the "drift check
// is live-only" property. When no live StatefulSet exists yet, the
// drift detector must return no pending migrations and let the normal
// reconcile path run — otherwise no database could be deployed for
// the first time.
func TestDatabases_FirstDeploySkipsDriftDetection(t *testing.T) {
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
				Engine: "postgres",
				Server: "master",
				Size:   20,
				Credentials: &config.DatabaseCredentialsDef{
					User:     "$U",
					Password: "$P",
					Database: "myapp",
				},
			},
		},
	}

	_, pending, err := Databases(context.Background(), dc, cfg,
		map[string]string{"U": "u", "P": "p"})
	if err != nil {
		t.Fatalf("first-deploy must succeed (no live StatefulSet), got: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("no pending migrations expected on first deploy, got %+v", pending)
	}
}

// TestDatabases_EmitsOperatorFacingLog locks the deploy-log contract:
// every database reconcile MUST open a `database/set/<name>` Command
// group and emit Successes for the steps that ran. Silent reconcile
// was a real production regression — operators thought databases
// weren't being reconciled at all because no log lines fired between
// `registry set` and `service set`. This test catches a future revert.
//
// Asserts on the actual MockOutput capture (Commands + Successes
// arrays) — not on stdout, not on the renderer. The Command/Success
// shape is the contract; renderers are downstream.
func TestDatabases_EmitsOperatorFacingLog(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{})
	cf.RegisterBucket("test-bucket")

	dc := testDC(convergeMock())
	dc.Creds = testCreds()
	dc.Storage = app.ProviderRef{
		Name:  "test-bucket",
		Creds: map[string]string{"api_key": "x", "account_id": "acct"},
	}
	out := dc.Cluster.Output.(*testutil.MockOutput)

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute", Storage: "test-bucket"},
		Databases: map[string]config.DatabaseDef{
			"app": {
				Engine: "postgres",
				Server: "master",
				Size:   20,
				Credentials: &config.DatabaseCredentialsDef{
					User:     "$U",
					Password: "$P",
					Database: "myapp",
				},
				Backup: &config.DatabaseBackupDef{Schedule: "0 3 * * *", Retention: 14},
			},
		},
	}

	if _, _, err := Databases(context.Background(), dc, cfg, map[string]string{"U": "u", "P": "p"}); err != nil {
		t.Fatalf("Databases: %v", err)
	}

	// Command group — operator sees "database set app" in the deploy log.
	wantCommand := "database/set/app"
	found := false
	for _, c := range out.Commands {
		if c == wantCommand {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("missing Command %q; got Commands=%v", wantCommand, out.Commands)
	}

	// Three Successes per DB with backup configured: bucket, credentials,
	// workloads. Substring match because we don't lock exact wording.
	wantContains := []string{
		"backup bucket",      // ensureDatabaseBackupBucket success
		"credentials secret", // EnsureCredentials success
		"applied",            // workloads applied
	}
	for _, want := range wantContains {
		hit := false
		for _, s := range out.Successes {
			if strings.Contains(s, want) {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("missing Success containing %q; got Successes=%v", want, out.Successes)
		}
	}
}

// TestDatabases_NoBackup_OnlyTwoSuccesses locks the partial-emission
// contract: a database WITHOUT backup config must NOT emit a "backup
// bucket" success (because no bucket gets provisioned).
func TestDatabases_NoBackup_OnlyTwoSuccesses(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{})
	cf.RegisterBucket("test-noop")

	dc := testDC(convergeMock())
	dc.Creds = testCreds()
	dc.Storage = app.ProviderRef{
		Name:  "test-noop",
		Creds: map[string]string{"api_key": "x", "account_id": "acct"},
	}
	out := dc.Cluster.Output.(*testutil.MockOutput)

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute"},
		Databases: map[string]config.DatabaseDef{
			"app": {
				Engine: "postgres",
				Server: "master",
				Size:   20,
				Credentials: &config.DatabaseCredentialsDef{
					User:     "$U",
					Password: "$P",
					Database: "myapp",
				},
			},
		},
	}
	if _, _, err := Databases(context.Background(), dc, cfg, map[string]string{"U": "u", "P": "p"}); err != nil {
		t.Fatalf("Databases: %v", err)
	}
	for _, s := range out.Successes {
		if strings.Contains(s, "backup bucket") {
			t.Errorf("must NOT emit backup-bucket Success when backup unconfigured; got %q", s)
		}
	}
	_ = utils.SortedKeys(map[string]string{}) // keeps utils import in use
}
