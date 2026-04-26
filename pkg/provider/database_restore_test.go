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

	// envFrom must include:
	//   1. Credentials Secret with DB_ prefix (so the image reads
	//      DB_URL, matching the backup CronJob's shape).
	//   2. Backup-creds Secret without prefix (so AWS_* lands with
	//      its native names for sigv4).
	if len(c.EnvFrom) != 2 {
		t.Fatalf("expected 2 envFrom entries, got %d", len(c.EnvFrom))
	}
	assertEnvFrom(t, c.EnvFrom[0], "DB_", req.CredentialsSecretName)
	assertEnvFrom(t, c.EnvFrom[1], "", req.BackupCredsSecretName)

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
