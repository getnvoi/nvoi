package planetscale_test

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/internal/testutil/kubefake"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// TestEnsureCredentials_WritesSecret locks the provisioning path:
// first call creates the database + mints a password (plain_text is
// only surfaced at creation), then writes the DSN into a cluster
// Secret the rest of reconcile trusts.
func TestEnsureCredentials_WritesSecret(t *testing.T) {
	fake := testutil.NewPlanetScaleFake(t)
	fake.Register("ps-test")
	p, err := provider.ResolveDatabase("ps-test", map[string]string{"service_token": "x", "organization": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	kf := kubefake.NewKubeFake()
	req := provider.DatabaseRequest{
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		Namespace:             "nvoi-myapp-prod",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
	}
	creds, err := p.EnsureCredentials(context.Background(), kf.Client, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(creds.URL, "mysql://") {
		t.Fatalf("url = %q", creds.URL)
	}
	if got, err := kf.GetSecretValue(context.Background(), req.Namespace, req.CredentialsSecretName, "url"); err != nil || got != creds.URL {
		t.Fatalf("secret url mismatch: got %q err %v", got, err)
	}
}

// TestExecSQL_HTTPDataAPI exercises the HTTP Data API path end-to-end:
// EnsureCredentials provisions + writes the cluster Secret; ExecSQL
// reads the Secret, HTTP-Basics the Data API (fake echoes the query
// back as a single row) and decodes the lengths+values format.
//
// This is the test that proves PlanetScale ExecSQL is no longer
// "unsupported" — the whole point of the unified backup/SQL contract.
func TestExecSQL_HTTPDataAPI(t *testing.T) {
	fake := testutil.NewPlanetScaleFake(t)
	fake.Register("ps-test-sql")
	p, err := provider.ResolveDatabase("ps-test-sql", map[string]string{"service_token": "x", "organization": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	kf := kubefake.NewKubeFake()
	req := provider.DatabaseRequest{
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		Namespace:             "nvoi-myapp-prod",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
		Kube:                  kf.Client,
	}
	if _, err := p.EnsureCredentials(context.Background(), kf.Client, req); err != nil {
		t.Fatal(err)
	}
	res, err := p.ExecSQL(context.Background(), req, "SELECT 1")
	if err != nil {
		t.Fatalf("ExecSQL: %v", err)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "query" {
		t.Fatalf("columns = %#v", res.Columns)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "SELECT 1" {
		t.Fatalf("rows = %#v (want single row echoing the query)", res.Rows)
	}
}

// TestReconcile_EmitsBackupCronJob locks the unified backup contract:
// when Spec.Backup is set, Reconcile emits a single CronJob that
// envFroms the credentials + backup-creds Secrets. The image, schedule,
// and envFrom wiring are what makes the dump pipeline uniform across
// providers.
func TestReconcile_EmitsBackupCronJob(t *testing.T) {
	fake := testutil.NewPlanetScaleFake(t)
	fake.Register("ps-test-reconcile")
	p, err := provider.ResolveDatabase("ps-test-reconcile", map[string]string{"service_token": "x", "organization": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	req := provider.DatabaseRequest{
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		BackupName:            "nvoi-myapp-prod-db-app-backup",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
		BackupCredsSecretName: "nvoi-myapp-prod-db-app-backup-creds",
		Namespace:             "nvoi-myapp-prod",
		Spec: provider.DatabaseSpec{
			Engine: "planetscale",
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
	if cj.Spec.Schedule != "0 3 * * *" {
		t.Fatalf("schedule = %q", cj.Spec.Schedule)
	}
	envFrom := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].EnvFrom
	if len(envFrom) != 2 {
		t.Fatalf("expected 2 envFrom sources (creds + backup-creds), got %d", len(envFrom))
	}
	if !containsSecretRef(envFrom, "nvoi-myapp-prod-db-app-credentials") {
		t.Fatalf("envFrom missing credentials secret: %#v", envFrom)
	}
	if !containsSecretRef(envFrom, "nvoi-myapp-prod-db-app-backup-creds") {
		t.Fatalf("envFrom missing backup-creds secret: %#v", envFrom)
	}
	env := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env
	if !containsEnv(env, "ENGINE", "planetscale") {
		t.Fatalf("ENGINE=planetscale missing in env: %#v", env)
	}
}

// TestBackupNow_CreatesJobFromCronJob locks the one-shot path. Reconcile
// must have applied the CronJob first (we seed it directly on the
// kubefake to isolate this test from Reconcile's apply loop); BackupNow
// instantiates a Job from that template.
func TestBackupNow_CreatesJobFromCronJob(t *testing.T) {
	fake := testutil.NewPlanetScaleFake(t)
	fake.Register("ps-test-backup-now")
	p, err := provider.ResolveDatabase("ps-test-backup-now", map[string]string{"service_token": "x", "organization": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	kf := kubefake.NewKubeFake()
	ns := "nvoi-myapp-prod"
	cronName := "nvoi-myapp-prod-db-app-backup"
	// Seed the CronJob directly — simulates what Reconcile+Apply would
	// have left on the cluster.
	_, _ = kf.Typed.BatchV1().CronJobs(ns).Create(context.Background(), &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: cronName, Namespace: ns},
		Spec:       batchv1.CronJobSpec{Schedule: "0 3 * * *", JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{}}},
	}, metav1.CreateOptions{})

	req := provider.DatabaseRequest{
		Name:       "app",
		FullName:   "nvoi-myapp-prod-db-app",
		BackupName: cronName,
		Namespace:  ns,
		Kube:       kf.Client,
		Spec: provider.DatabaseSpec{
			Engine: "planetscale",
			Backup: &provider.DatabaseBackupSpec{Schedule: "0 3 * * *"},
		},
	}
	ref, err := p.BackupNow(context.Background(), req)
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}
	if ref.ID == "" || ref.Kind != "dump" {
		t.Fatalf("ref = %#v", ref)
	}
	// The Job is created by kc.CreateJobFromCronJob — assert via the
	// typed tracker.
	if _, err := kf.Typed.BatchV1().Jobs(ns).Get(context.Background(), ref.ID, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected one-shot Job %s to exist: %v", ref.ID, err)
	}
}

func containsSecretRef(sources []corev1.EnvFromSource, name string) bool {
	for _, s := range sources {
		if s.SecretRef != nil && s.SecretRef.Name == name {
			return true
		}
	}
	return false
}

func containsEnv(env []corev1.EnvVar, name, value string) bool {
	for _, e := range env {
		if e.Name == name && e.Value == value {
			return true
		}
	}
	return false
}
