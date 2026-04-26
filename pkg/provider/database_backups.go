// Shared database backup + restore pipeline.
//
// Every DatabaseProvider produces backups the same way: gzipped logical
// dumps land in `nvoi-{app}-{env}-db-{name}-backups`, pushed by a CronJob
// (scheduled) or a one-shot Job (manual via `nvoi database backup now`).
// Restore is symmetric: a one-shot Job pulls an object, gunzips, and
// pipes into the engine's native restore tool against $DATABASE_URL.
// Pull/put mechanics vary by engine; direction flips via MODE env var.
// `ListBackups` / `DownloadBackup` walk the bucket directly and are
// engine-agnostic.
//
// The uniform image (`docker.io/nvoi/db:<cli-version>`) carries
// pg_dump + psql + mysqldump + mysql + gzip + a sigv4-aware client
// and dispatches based on `MODE` (backup | restore) and `ENGINE`. The
// image's source is `cmd/db/`; the publish workflow lives in
// `.github/workflows/release.yml`.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

// DBImageRepo is the registry path of the uniform backup + restore
// container image. Built from cmd/db/Dockerfile, published to Docker
// Hub on every `v*` tag by .github/workflows/release.yml AND on every
// local `bin/deploy` (operators with push perms). Public repo, no
// auth required for pull.
//
// Lives here, not in pkg/utils/naming.go: the image is external
// infrastructure nvoi REFERENCES, not a resource nvoi creates. The
// repo path matches whatever bin/deploy passes to docker buildx.
const DBImageRepo = "docker.io/nvoi/db"

// DBImageTag is the tag nvoi appends to DBImageRepo. Overridden at
// build time:
//
//	-ldflags "-X github.com/getnvoi/nvoi/pkg/provider.DBImageTag=v1.2.3"
//
// set in .github/workflows/release.yml on every `v*` tag. Default
// "latest" makes local builds (bin/nvoi) pull the most recent stable
// image — release.yml publishes both `:vX.Y.Z` and `:latest` per
// release. Tagged builds inject the pinned tag so prod CLI and image
// stay in lockstep.
var DBImageTag = "latest"

// DBImage is the unresolved (`:tag`) reference. Used as a fallback
// when DatabaseRequest.DBImageRef is empty — tests that build Jobs
// without exercising a live registry, or callers that explicitly
// don't need digest-pinning. Production reconcile always resolves
// via ResolveDBImage and threads the digest-pinned ref through
// req.DBImageRef.
func DBImage() string {
	return DBImageRepo + ":" + DBImageTag
}

// dbImageFor returns req.DBImageRef when set, else DBImage(). Single
// source of "what image string lands on the CronJob/Job" so the
// fallback rule lives in one place.
func dbImageFor(req DatabaseRequest) string {
	if req.DBImageRef != "" {
		return req.DBImageRef
	}
	return DBImage()
}

// dbCredsEnv returns the explicit env-var mappings the cmd/db image's
// entrypoint reads, sourcing each from the credentials Secret named
// `secret`. Used by BuildBackupCronJob and BuildRestoreJob.
//
// Why this isn't an `envFrom prefix=DB_`: Kubernetes envFrom binds
// each Secret key as an env var with name = prefix + key, *verbatim*
// — keys are NOT uppercased. The Go-side providers write the
// credentials Secret with lowercase keys (`url`, `host`, `user`, …)
// because every other call site reads them via
// `kc.GetSecretValue(..., "url")`. envFrom-prefix would therefore
// produce `DB_url` (lowercase), and cmd/db's `mustEnv("DB_URL")`
// would always fail at runtime — silently, until someone looks at
// pod logs hours later.
//
// Why every field is bound (not just DB_URL): cmd/db used to take
// the URL alone and re-parse it back into host/user/password/
// database for mysqldump's `-h/-u/--password=` flags. That parser
// (parseMySQLDSN, deleted) silently dropped the port and any
// query-string options — info the Secret already carries as
// dedicated keys. Round-trip waste AND lossy. cmd/db now reads
// each field directly from its corresponding env var; the parser
// is gone.
//
// Mapping locked by the per-provider TestReconcile_EmitsBackupCronJob
// suites + TestBuildBackupCronJob_DBURLBinding /
// TestBuildRestoreJob_HonorsDBImageRef in pkg/provider — any future
// regression on the env contract fails at unit-test time.
func dbCredsEnv(secret string) []corev1.EnvVar {
	ref := func(envName, secretKey string) corev1.EnvVar {
		return corev1.EnvVar{
			Name: envName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secret},
					Key:                  secretKey,
				},
			},
		}
	}
	return []corev1.EnvVar{
		ref("DB_URL", "url"),
		ref("DB_HOST", "host"),
		ref("DB_PORT", "port"),
		ref("DB_USER", "user"),
		ref("DB_PASSWORD", "password"),
		ref("DB_DATABASE", "database"),
		ref("DB_SSLMODE", "sslmode"),
	}
}

// ResolveDBImage returns the digest-pinned reference
// (`docker.io/nvoi/db@sha256:<digest>`) for DBImageTag by HEAD-ing
// the Docker Hub manifest endpoint. Reconcile calls this once per
// deploy and threads the result into every DatabaseRequest, so
// kubelet sees a fresh image string whenever the underlying image
// content changed. `:latest` alone never invalidates the kubelet
// cache — a single bad push leaves backup pods in ImagePullBackOff
// indefinitely.
//
// Failure modes (all hard errors — better to fail the deploy than
// apply a CronJob whose image we couldn't verify):
//   - tag not found on Docker Hub → push silently failed, pre-deploy
//   - registry unreachable      → fix internet, then redeploy
//   - missing digest header     → registry implementation oddity
//
// Package var so reconcile/cmd tests can stub the registry round-trip
// without hitting the network.
var ResolveDBImage = registryResolveDBImage

func registryResolveDBImage(ctx context.Context) (string, error) {
	digest, err := registryDigest(ctx, "nvoi/db", DBImageTag)
	if err != nil {
		return "", fmt.Errorf("resolve %s:%s digest: %w", DBImageRepo, DBImageTag, err)
	}
	return DBImageRepo + "@" + digest, nil
}

// registryDigest queries Docker Hub for the current
// `Docker-Content-Digest` of <repo>:<tag>. Anonymous, public-repo
// only — nvoi/db is published as a public image. Two-step protocol:
//
//  1. GET auth.docker.io/token?service=registry.docker.io&scope=...
//     → bearer token
//  2. HEAD registry-1.docker.io/v2/<repo>/manifests/<tag> with the
//     Bearer token + Accept covering OCI image-index AND Docker
//     manifest-list (buildx pushes OCI; older registries / buildx
//     versions may push Docker — both shapes accepted).
func registryDigest(ctx context.Context, repo, tag string) (string, error) {
	tokenURL := fmt.Sprintf(
		"https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s:pull",
		repo,
	)
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	tokenResp, err := http.DefaultClient.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("fetch token: %w", err)
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d", tokenResp.StatusCode)
	}
	var tokenBody struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenBody); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if tokenBody.Token == "" {
		return "", fmt.Errorf("empty token")
	}

	manifestURL := fmt.Sprintf("https://registry-1.docker.io/v2/%s/manifests/%s", repo, tag)
	manReq, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build manifest request: %w", err)
	}
	manReq.Header.Set("Authorization", "Bearer "+tokenBody.Token)
	manReq.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	manResp, err := http.DefaultClient.Do(manReq)
	if err != nil {
		return "", fmt.Errorf("HEAD manifest: %w", err)
	}
	defer manResp.Body.Close()
	if manResp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("tag %s:%s not found — was the image actually pushed? (try `docker buildx imagetools inspect %s:%s`)",
			repo, tag, repo, tag)
	}
	if manResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("manifest %s:%s returned %d", repo, tag, manResp.StatusCode)
	}
	digest := manResp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("missing Docker-Content-Digest header for %s:%s", repo, tag)
	}
	return digest, nil
}

// BuildBackupCronJob returns the uniform CronJob that dumps a database
// and uploads the gzipped result to the bucket named in req.Bucket.
// Every DatabaseProvider whose Spec.Backup is set embeds this CronJob
// in the Workloads it returns from Reconcile.
//
// Requirements on req:
//   - Spec.Backup.Schedule is a valid cron expression (validated upstream).
//   - Bucket carries the S3-compatible endpoint/key material.
//   - CredentialsSecretName exists and contains a `url` key (the DSN).
//   - BackupCredsSecretName exists and contains BUCKET_* + AWS_*.
//
// The CronJob's Command is the image's default entrypoint — the image
// reads ENGINE + DATABASE_URL + BUCKET_* from the envFrom'd Secrets and
// picks the right dump tool.
func BuildBackupCronJob(req DatabaseRequest) *batchv1.CronJob {
	// Ownership (nvoi/owner=databases) is stamped at apply time by
	// kube.Client.ApplyOwned. This builder only sets the per-workload
	// identity labels (app.kubernetes.io/name) plus whatever the
	// caller passed via req.Labels.
	labels := map[string]string{
		utils.LabelAppName:      req.FullName,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
	}
	for k, v := range req.Labels {
		if _, exists := labels[k]; !exists {
			labels[k] = v
		}
	}

	backoff := int32(2)
	successHistory := int32(3)
	failureHistory := int32(3)

	return &batchv1.CronJob{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "CronJob"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.BackupName,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.CronJobSpec{
			Schedule:                   req.Spec.Backup.Schedule,
			SuccessfulJobsHistoryLimit: &successHistory,
			FailedJobsHistoryLimit:     &failureHistory,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					BackoffLimit: &backoff,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{{
								Name:  "backup",
								Image: dbImageFor(req),
								Env: append(
									[]corev1.EnvVar{
										{Name: "ENGINE", Value: req.Spec.Engine},
										{Name: "DATABASE_NAME", Value: req.Name},
										{Name: "DATABASE_FULL_NAME", Value: req.FullName},
									},
									// DB_URL: explicit mapping from the credentials
									// Secret's `url` key. NOT envFrom-prefix —
									// envFrom doesn't uppercase keys, so prefix
									// "DB_" + key "url" produces env var "DB_url"
									// (lowercase), which doesn't match cmd/db's
									// mustEnv("DB_URL"). The lowercase Secret keys
									// can't change without ripping up every
									// GetSecretValue("...", "url") read site, so
									// the explicit Name → SecretKeyRef.Key mapping
									// is the correct discipline here. Same in
									// BuildRestoreJob.
									dbCredsEnv(req.CredentialsSecretName)...,
								),
								EnvFrom: []corev1.EnvFromSource{
									// Bucket creds: BUCKET_ENDPOINT / BUCKET_NAME /
									// AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY /
									// AWS_REGION. envFrom works here because the
									// bucket Secret's keys are already uppercase
									// (BuildBackupCredsSecretData). No prefix —
									// sigv4 tooling reads the AWS_* names directly.
									{
										SecretRef: &corev1.SecretEnvSource{
											LocalObjectReference: corev1.LocalObjectReference{Name: req.BackupCredsSecretName},
										},
									},
								},
							}},
						},
					},
				},
			},
		},
	}
}

// BuildRestoreJob returns a one-shot Job that replays a bucket-resident
// backup artifact into the database. Mirrors BuildBackupCronJob's pod
// spec — same image, same envFrom Secrets — with two additions:
//
//   - MODE=restore flips the image's dispatch to the restore pipeline.
//   - BACKUP_KEY names the bucket object to pull.
//
// Used by RunRestoreJob below, which is what every DatabaseProvider's
// Restore method calls. Single source of truth for the restore Job's
// shape; engine-specificity (psql vs mysql) lives in the image's
// dispatch, same layering as backup.
//
// The Job is named deterministically with a unix timestamp suffix so
// concurrent restores from different operators don't collide. The
// caller (RunRestoreJob) waits for the Job to succeed before returning.
func BuildRestoreJob(req DatabaseRequest, backupKey string) *batchv1.Job {
	// Same labeling discipline as BuildBackupCronJob — ownership is
	// stamped at apply time by ApplyOwned. `nvoi/restore-of` ties the
	// Job back to the source DB for log/debug navigation.
	labels := map[string]string{
		utils.LabelAppName:      req.FullName,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
		"nvoi/restore-of":       req.Name,
	}
	for k, v := range req.Labels {
		if _, exists := labels[k]; !exists {
			labels[k] = v
		}
	}

	backoff := int32(0) // restores don't auto-retry — a failed restore
	// leaves the DB in an unknown state; caller decides what to do.

	// Job name embeds a unix timestamp so concurrent `restore` calls
	// from different operators (or a retry after a crash) don't collide
	// on the same name.
	jobName := fmt.Sprintf("%s-restore-%d", req.FullName, time.Now().Unix())

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  "restore",
						Image: dbImageFor(req),
						Env: append(
							[]corev1.EnvVar{
								{Name: "MODE", Value: "restore"},
								{Name: "BACKUP_KEY", Value: backupKey},
								{Name: "ENGINE", Value: req.Spec.Engine},
								{Name: "DATABASE_NAME", Value: req.Name},
								{Name: "DATABASE_FULL_NAME", Value: req.FullName},
							},
							// DB_URL: explicit mapping — see BuildBackupCronJob
							// for why envFrom-prefix doesn't work here (envFrom
							// keys aren't uppercased).
							dbCredsEnv(req.CredentialsSecretName)...,
						),
						EnvFrom: []corev1.EnvFromSource{
							// Bucket creds: BUCKET_ENDPOINT / BUCKET_NAME /
							// AWS_* for the sigv4 download. Uppercase keys =
							// envFrom-clean.
							{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: req.BackupCredsSecretName},
								},
							},
						},
					}},
				},
			},
		},
	}
}

// RunRestoreJob applies the restore Job and blocks until it completes.
// Shared across every DatabaseProvider — each provider's Restore method
// is a one-liner calling this helper. On Job failure, WaitForJob
// returns an error with the pod's recent logs attached, which is what
// the operator sees on the CLI.
func RunRestoreJob(ctx context.Context, kc *kube.Client, req DatabaseRequest, backupKey string) error {
	if kc == nil {
		return fmt.Errorf("restore requires kube client")
	}
	if req.BackupCredsSecretName == "" || req.Bucket == nil {
		return fmt.Errorf("restore requires providers.storage + a backup bucket (did providers.storage get unset between backup and restore?)")
	}
	job := BuildRestoreJob(req, backupKey)
	if err := kc.ApplyOwned(ctx, req.Namespace, utils.OwnerDatabases, job); err != nil {
		return fmt.Errorf("apply restore job %s: %w", job.Name, err)
	}
	var progress kube.ProgressEmitter
	if req.Log != nil {
		progress = req.Log
	}
	if err := kc.WaitForJob(ctx, req.Namespace, job.Name, progress); err != nil {
		return fmt.Errorf("restore job %s: %w", job.Name, err)
	}
	return nil
}

// BucketListBackups enumerates every dump in the database's backup
// bucket. Used by postgres, neon, and planetscale — `DatabaseProvider`
// implementations delegate to this helper instead of each rolling their
// own object-store loop. Kind is always "dump" (uniform pipeline =
// uniform artifact shape).
func BucketListBackups(_ context.Context, req DatabaseRequest) ([]BackupRef, error) {
	if req.Bucket == nil {
		return nil, fmt.Errorf("backups require providers.storage + a backup bucket")
	}
	objs, err := s3.ListObjects(
		req.Bucket.Credentials.Endpoint,
		req.Bucket.Credentials.AccessKeyID,
		req.Bucket.Credentials.SecretAccessKey,
		req.Bucket.Name,
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("list backups for %s: %w", req.Name, err)
	}
	out := make([]BackupRef, 0, len(objs))
	for _, o := range objs {
		out = append(out, BackupRef{
			ID:        o.Key,
			CreatedAt: o.LastModified,
			SizeBytes: o.Size,
			Kind:      "dump",
		})
	}
	return out, nil
}

// BucketDownloadBackup streams a single dump to the writer. Shared
// implementation across every DatabaseProvider — the pipeline's
// uniformity makes list/download provider-agnostic.
func BucketDownloadBackup(_ context.Context, req DatabaseRequest, id string, w io.Writer) error {
	if req.Bucket == nil {
		return fmt.Errorf("backups require providers.storage + a backup bucket")
	}
	rc, _, _, err := s3.GetStream(
		req.Bucket.Credentials.Endpoint,
		req.Bucket.Credentials.AccessKeyID,
		req.Bucket.Credentials.SecretAccessKey,
		req.Bucket.Name,
		id,
	)
	if err != nil {
		return fmt.Errorf("download backup %s/%s: %w", req.Bucket.Name, id, err)
	}
	defer rc.Close()
	if _, err := io.Copy(w, rc); err != nil {
		return fmt.Errorf("stream backup %s: %w", id, err)
	}
	return nil
}

// BuildBackupCredsSecretData materializes the bucket credentials into the
// env-var shape the backup image expects. Kept here (not in the bucket
// package) because this is the one place nvoi crosses the boundary from
// "bucket credentials" to "backup image contract" — renaming a key here
// breaks the image's entrypoint script and nothing else.
func BuildBackupCredsSecretData(bucketName string, creds BucketCredentials) map[string]string {
	region := creds.Region
	if region == "" {
		region = "auto"
	}
	return map[string]string{
		"BUCKET_ENDPOINT":       strings.TrimRight(creds.Endpoint, "/"),
		"BUCKET_NAME":           bucketName,
		"AWS_ACCESS_KEY_ID":     creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY": creds.SecretAccessKey,
		"AWS_REGION":            region,
	}
}
