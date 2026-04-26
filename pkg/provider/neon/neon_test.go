package neon_test

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/internal/testutil/kubefake"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestEnsureCredentials_CreatesProjectAndWritesSecret(t *testing.T) {
	fake := testutil.NewNeonFake(t)
	fake.Register("neon-test")
	p, err := provider.ResolveDatabase("neon-test", map[string]string{"api_key": "x"})
	if err != nil {
		t.Fatal(err)
	}
	kf := kubefake.NewKubeFake()
	req := provider.DatabaseRequest{
		Name:                  "analytics",
		FullName:              "nvoi-myapp-prod-db-analytics",
		Namespace:             "nvoi-myapp-prod",
		CredentialsSecretName: "nvoi-myapp-prod-db-analytics-credentials",
		Spec:                  provider.DatabaseSpec{Region: "eu-central-1"},
	}
	creds, err := p.EnsureCredentials(context.Background(), kf.Client, req)
	if err != nil {
		t.Fatalf("EnsureCredentials: %v", err)
	}
	if creds.Password == "" || creds.Host == "" {
		t.Fatalf("creds = %#v", creds)
	}
	got, err := kf.GetSecretValue(context.Background(), req.Namespace, req.CredentialsSecretName, "password")
	if err != nil {
		t.Fatal(err)
	}
	if got != creds.Password {
		t.Fatalf("secret password = %q, want %q", got, creds.Password)
	}
}

func TestExecSQL_RoundTrip(t *testing.T) {
	fake := testutil.NewNeonFake(t)
	fake.Register("neon-test-sql")
	p, err := provider.ResolveDatabase("neon-test-sql", map[string]string{"api_key": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.EnsureCredentials(context.Background(), nil, provider.DatabaseRequest{FullName: "nvoi-myapp-prod-db-analytics"}); err != nil {
		t.Fatal(err)
	}
	res, err := p.ExecSQL(context.Background(), provider.DatabaseRequest{FullName: "nvoi-myapp-prod-db-analytics"}, "SELECT 1")
	if err != nil {
		t.Fatalf("ExecSQL: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
		t.Fatalf("rows = %#v", res.Rows)
	}
}

// Reconcile emits the shared backup CronJob when backups are configured
// — same contract as postgres and planetscale. Asserts the uniform
// envFrom wiring so a future tweak of one provider's CronJob body
// doesn't silently diverge from the others.
func TestReconcile_EmitsBackupCronJob(t *testing.T) {
	fake := testutil.NewNeonFake(t)
	fake.Register("neon-test-reconcile")
	p, err := provider.ResolveDatabase("neon-test-reconcile", map[string]string{"api_key": "x"})
	if err != nil {
		t.Fatal(err)
	}
	req := provider.DatabaseRequest{
		Name:                  "analytics",
		FullName:              "nvoi-myapp-prod-db-analytics",
		BackupName:            "nvoi-myapp-prod-db-analytics-backup",
		CredentialsSecretName: "nvoi-myapp-prod-db-analytics-credentials",
		BackupCredsSecretName: "nvoi-myapp-prod-db-analytics-backup-creds",
		Namespace:             "nvoi-myapp-prod",
		Spec: provider.DatabaseSpec{
			Engine: "neon",
			Backup: &provider.DatabaseBackupSpec{Schedule: "0 3 * * *", Retention: 14},
		},
	}
	plan, err := p.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Workloads) != 1 {
		t.Fatalf("expected 1 workload (CronJob), got %d", len(plan.Workloads))
	}
	cj, ok := plan.Workloads[0].(*batchv1.CronJob)
	if !ok {
		t.Fatalf("expected *batchv1.CronJob, got %T", plan.Workloads[0])
	}
	env := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env
	if !envContains(env, "ENGINE", "neon") {
		t.Fatalf("ENGINE=neon missing in env: %#v", env)
	}
	// Every DB_* env var cmd/db reads must source from the
	// credentials Secret's matching lowercase key. Neon writes
	// every key including `port` (5432) so the binding works
	// uniformly across selfhosted + SaaS — no provider-specific
	// branching in the spec or in cmd/db.
	for _, b := range []struct{ envName, secretKey string }{
		{"DB_URL", "url"},
		{"DB_HOST", "host"},
		{"DB_PORT", "port"},
		{"DB_USER", "user"},
		{"DB_PASSWORD", "password"},
		{"DB_DATABASE", "database"},
		{"DB_SSLMODE", "sslmode"},
	} {
		var found bool
		for _, e := range env {
			if e.Name == b.envName && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil &&
				e.ValueFrom.SecretKeyRef.Name == "nvoi-myapp-prod-db-analytics-credentials" &&
				e.ValueFrom.SecretKeyRef.Key == b.secretKey {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not bound to credentials Secret's %q key — cmd/db's mustEnv(%q) will fail at runtime: %#v",
				b.envName, b.secretKey, b.envName, env)
		}
	}
}

// Reconcile returns no workloads when backups aren't configured — Neon
// is fully API-driven, so there's nothing else to provision in-cluster.
func TestReconcile_NoWorkloadsWhenBackupUnset(t *testing.T) {
	fake := testutil.NewNeonFake(t)
	fake.Register("neon-test-no-backup")
	p, err := provider.ResolveDatabase("neon-test-no-backup", map[string]string{"api_key": "x"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Reconcile(context.Background(), provider.DatabaseRequest{Name: "analytics"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Workloads) != 0 {
		t.Fatalf("expected 0 workloads, got %d", len(plan.Workloads))
	}
}

// BackupNow instantiates a one-shot Job from the scheduled CronJob.
// This is the uniform "kick a backup right now" path — same shape in
// every DatabaseProvider, no branch-creation fork for neon anymore.
func TestBackupNow_CreatesJobFromCronJob(t *testing.T) {
	fake := testutil.NewNeonFake(t)
	fake.Register("neon-test-backup-now")
	p, err := provider.ResolveDatabase("neon-test-backup-now", map[string]string{"api_key": "x"})
	if err != nil {
		t.Fatal(err)
	}
	kf := kubefake.NewKubeFake()
	ns := "nvoi-myapp-prod"
	cronName := "nvoi-myapp-prod-db-analytics-backup"
	_, _ = kf.Typed.BatchV1().CronJobs(ns).Create(context.Background(), &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: cronName, Namespace: ns},
		Spec:       batchv1.CronJobSpec{Schedule: "0 3 * * *", JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{}}},
	}, metav1.CreateOptions{})

	req := provider.DatabaseRequest{
		Name:       "analytics",
		FullName:   "nvoi-myapp-prod-db-analytics",
		BackupName: cronName,
		Namespace:  ns,
		Kube:       kf.Client,
		Spec: provider.DatabaseSpec{
			Engine: "neon",
			Backup: &provider.DatabaseBackupSpec{Schedule: "0 3 * * *"},
		},
	}
	ref, err := p.BackupNow(context.Background(), req)
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}
	if ref.ID == "" || ref.Kind != "dump" {
		t.Fatalf("ref = %#v (want Kind=dump, non-empty ID)", ref)
	}
	if _, err := kf.Typed.BatchV1().Jobs(ns).Get(context.Background(), ref.ID, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected one-shot Job %s to exist: %v", ref.ID, err)
	}
}

func envContains(env []corev1.EnvVar, name, value string) bool {
	for _, e := range env {
		if e.Name == name && e.Value == value {
			return true
		}
	}
	return false
}
