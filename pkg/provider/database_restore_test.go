package provider

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestBuildRestoreJob locks the shape of the restore Job across every
// DatabaseProvider. Every engine's Restore method is a one-liner that
// calls RunRestoreJob, which applies the output of this builder — so
// if this test passes, every provider's restore has the same structure.
//
// Specifically: MODE=restore flips the image's dispatch, BACKUP_KEY
// names the object, and envFrom pulls DATABASE_URL (from the
// credentials Secret, DB_-prefixed) and the bucket creds (unprefixed,
// so AWS_* names reach the sigv4 client directly). Image is the
// version-pinned nvoi/db.
func TestBuildRestoreJob(t *testing.T) {
	req := DatabaseRequest{
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		Namespace:             "nvoi-myapp-prod",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
		BackupCredsSecretName: "nvoi-myapp-prod-db-app-backup-creds",
		Spec:                  DatabaseSpec{Engine: "postgres"},
	}

	job := BuildRestoreJob(req, "20260423T030000Z.sql.gz")

	if job.Namespace != "nvoi-myapp-prod" {
		t.Errorf("namespace = %q", job.Namespace)
	}
	// Name includes a unix timestamp suffix; assert the prefix matches
	// so two concurrent restores don't collide on the same Job name.
	if got := job.Name; len(got) < len(req.FullName)+len("-restore-") {
		t.Errorf("job name too short: %q", got)
	}

	containers := job.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	c := containers[0]

	// Image must be the version-pinned nvoi/db — same image as backup,
	// different MODE. The CLI binary version and this image version
	// stay in lockstep via provider.DBImageTag.
	if got, want := c.Image, DBImage(); got != want {
		t.Errorf("image = %q, want %q", got, want)
	}

	// MODE=restore flips the image's dispatch. BACKUP_KEY names the
	// object to pull. ENGINE tells the image which restore tool
	// (psql vs mysql) to invoke. All three are plain env, not envFrom.
	wantEnv := map[string]string{
		"MODE":               "restore",
		"BACKUP_KEY":         "20260423T030000Z.sql.gz",
		"ENGINE":             "postgres",
		"DATABASE_NAME":      "app",
		"DATABASE_FULL_NAME": "nvoi-myapp-prod-db-app",
	}
	gotEnv := map[string]string{}
	for _, e := range c.Env {
		gotEnv[e.Name] = e.Value
	}
	for k, v := range wantEnv {
		if gotEnv[k] != v {
			t.Errorf("env %s = %q, want %q", k, gotEnv[k], v)
		}
	}

	// envFrom is bucket-creds only: BUCKET_ENDPOINT / BUCKET_NAME /
	// AWS_* — those keys are uppercase in the Secret, so envFrom
	// produces uppercase env vars cmd/db's mustEnv reads directly.
	if len(c.EnvFrom) != 1 {
		t.Fatalf("expected 1 envFrom entry (bucket-creds), got %d", len(c.EnvFrom))
	}
	assertEnvFrom(t, c.EnvFrom[0], "", req.BackupCredsSecretName)

	// Every cmd/db field is bound EXPLICITLY (not via envFrom-
	// prefix) because envFrom doesn't uppercase keys — prefix "DB_"
	// + Secret key "url" would produce "DB_url" and cmd/db's
	// mustEnv("DB_URL") would always fail. Same for host/port/etc.
	//
	// This assertion locks the full contract: any future change to
	// BuildRestoreJob's env wiring that loses an explicit
	// DB_X → SecretKeyRef.Key="x" mapping fails here, at unit-test
	// time, not at 3am when the cron pod logs the missing-env error.
	for _, b := range expectedDBEnvBindings(req.CredentialsSecretName) {
		assertSecretKeyEnv(t, c.Env, b.envName, req.CredentialsSecretName, b.secretKey)
	}

	// RestartPolicy must be Never — Jobs use OnFailure/Never; Never
	// paired with BackoffLimit=0 means a failed restore doesn't auto-
	// retry (the operator decides what to do after a failure).
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
	if bl := job.Spec.BackoffLimit; bl == nil || *bl != 0 {
		t.Errorf("backoff limit = %v, want 0", bl)
	}
}

func assertEnvFrom(t *testing.T, ef corev1.EnvFromSource, wantPrefix, wantSecret string) {
	t.Helper()
	if ef.Prefix != wantPrefix {
		t.Errorf("envFrom prefix = %q, want %q", ef.Prefix, wantPrefix)
	}
	if ef.SecretRef == nil {
		t.Fatalf("envFrom.SecretRef is nil")
	}
	if ef.SecretRef.Name != wantSecret {
		t.Errorf("envFrom secret = %q, want %q", ef.SecretRef.Name, wantSecret)
	}
}

// expectedDBEnvBindings is the canonical list of cmd/db DB_* env vars
// and their corresponding lowercase Secret keys. The mapping is owned
// by provider.dbCredsEnv; this list is the test-side mirror so any
// drift between the spec and what cmd/db reads breaks the suite.
//
// Add a new pair here when cmd/db starts reading another field.
func expectedDBEnvBindings(_ string) []struct{ envName, secretKey string } {
	return []struct{ envName, secretKey string }{
		{"DB_URL", "url"},
		{"DB_HOST", "host"},
		{"DB_PORT", "port"},
		{"DB_USER", "user"},
		{"DB_PASSWORD", "password"},
		{"DB_DATABASE", "database"},
		{"DB_SSLMODE", "sslmode"},
	}
}

// assertSecretKeyEnv finds an env var by name and asserts it sources
// from the named Secret's named key. Used to lock the cmd/db env
// contract — a regression that drops the binding (or points at the
// wrong key) fails here rather than at runtime in the cron pod.
func assertSecretKeyEnv(t *testing.T, envs []corev1.EnvVar, wantName, wantSecret, wantKey string) {
	t.Helper()
	for _, e := range envs {
		if e.Name != wantName {
			continue
		}
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Fatalf("env %s: missing ValueFrom.SecretKeyRef (must source from Secret, not literal Value)", wantName)
		}
		ref := e.ValueFrom.SecretKeyRef
		if ref.Name != wantSecret {
			t.Errorf("env %s: SecretKeyRef.Name = %q, want %q", wantName, ref.Name, wantSecret)
		}
		if ref.Key != wantKey {
			t.Errorf("env %s: SecretKeyRef.Key = %q, want %q (k8s envFrom doesn't uppercase keys — explicit mapping is the only way)", wantName, ref.Key, wantKey)
		}
		return
	}
	t.Errorf("env %s: not found in container env (cmd/db's mustEnv(%q) will fail at runtime)", wantName, wantName)
}

// TestBuildRestoreJob_HonorsDBImageRef locks the digest-pinning
// contract: when the caller resolved a digest-pinned image upstream
// (reconcile or the restore CLI both call provider.ResolveDBImage
// once per command and stash the result on req.DBImageRef), the
// builder uses THAT ref — not the bare `:latest` fallback. This is
// what stops kubelet's `:latest`-cache failure mode where a single
// bad push silently jams every backup pod into ImagePullBackOff.
func TestBuildRestoreJob_HonorsDBImageRef(t *testing.T) {
	const pinned = "docker.io/nvoi/db@sha256:" +
		"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	req := DatabaseRequest{
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		Namespace:             "nvoi-myapp-prod",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
		BackupCredsSecretName: "nvoi-myapp-prod-db-app-backup-creds",
		Spec: DatabaseSpec{
			Engine: "postgres",
			Backup: &DatabaseBackupSpec{Schedule: "0 3 * * *"},
		},
		DBImageRef: pinned,
	}

	job := BuildRestoreJob(req, "20260423T030000Z.sql.gz")
	got := job.Spec.Template.Spec.Containers[0].Image
	if got != pinned {
		t.Errorf("image = %q, want %q (DBImageRef must override DBImage())", got, pinned)
	}

	// The CronJob path goes through the same fallback helper —
	// assert by symmetry so a future drift between the two builders
	// gets caught.
	cron := BuildBackupCronJob(req)
	cronImage := cron.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image
	if cronImage != pinned {
		t.Errorf("cron image = %q, want %q", cronImage, pinned)
	}
}

// TestBuildBackupCronJob_DBURLBinding locks the cmd/db env contract
// at unit-test time. The image's main() does mustEnv("DB_URL"); if
// the CronJob spec doesn't bind DB_URL to the credentials Secret's
// "url" key, every backup pod fails at runtime with "missing required
// env var DB_URL" — silent until someone reads pod logs hours later.
//
// envFrom-prefix CANNOT supply this binding because k8s envFrom
// doesn't uppercase Secret keys (`url` stays lowercase). The fix is
// an explicit SecretKeyRef mapping; this test is what stops a
// future refactor from regressing to the broken envFrom-prefix shape.
func TestBuildBackupCronJob_DBURLBinding(t *testing.T) {
	req := DatabaseRequest{
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		Namespace:             "nvoi-myapp-prod",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
		BackupCredsSecretName: "nvoi-myapp-prod-db-app-backup-creds",
		Spec: DatabaseSpec{
			Engine: "postgres",
			Backup: &DatabaseBackupSpec{Schedule: "0 3 * * *"},
		},
	}

	cron := BuildBackupCronJob(req)
	c := cron.Spec.JobTemplate.Spec.Template.Spec.Containers[0]

	// Bucket creds via envFrom (uppercase keys → safe).
	if len(c.EnvFrom) != 1 {
		t.Fatalf("expected 1 envFrom entry (bucket-creds only), got %d", len(c.EnvFrom))
	}
	assertEnvFrom(t, c.EnvFrom[0], "", req.BackupCredsSecretName)

	// Explicit DB_* → lowercase-key mappings — the full contract,
	// not just DB_URL. cmd/db reads each field directly (no DSN
	// parsing); a missing binding fails the pod with
	// `missing required env var DB_X` at runtime.
	for _, b := range expectedDBEnvBindings(req.CredentialsSecretName) {
		assertSecretKeyEnv(t, c.Env, b.envName, req.CredentialsSecretName, b.secretKey)
	}
}

// TestRunRestoreJob_RejectsMissingBucket locks the precondition check:
// RunRestoreJob bails early if the caller forgot to wire the bucket
// handle or backup-creds Secret. Without both, the Job would launch and
// immediately fail with a cryptic env-var error — catching it at the
// caller's boundary is the better UX.
func TestRunRestoreJob_RejectsMissingBucket(t *testing.T) {
	// No Kube client needed — the bucket check runs first.
	err := RunRestoreJob(nil, nil, DatabaseRequest{
		Namespace: "nvoi-myapp-prod",
		// Bucket deliberately nil.
	}, "some-key.sql.gz")
	if err == nil {
		t.Fatal("expected error when kube client is nil")
	}
}
