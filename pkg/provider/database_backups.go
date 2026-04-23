// Shared database backup pipeline.
//
// Every DatabaseProvider produces backups the same way: gzipped logical
// dumps land in `nvoi-{app}-{env}-db-{name}-backups`, pushed by a CronJob
// (scheduled) or a one-shot Job (manual via `nvoi database backup now`).
// Pull mechanics vary by engine (pg_dump / mysqldump, in-cluster Service
// for selfhosted, external TLS endpoint for SaaS); put mechanics are
// identical. `ListBackups` / `DownloadBackup` walk the bucket directly
// and are engine-agnostic.
//
// The uniform image (`docker.io/nvoi/backup:<cli-version>`) carries
// pg_dump + mysqldump + gzip + a sigv4-aware uploader and dispatches
// based on the `ENGINE` env var. The image's source is `cmd/backup/`;
// the publish workflow lives in `.github/workflows/release.yml`.
package provider

import (
	"context"
	"fmt"
	"io"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

// BackupImage is the uniform image reference for the backup CronJob /
// one-shot Job. Tagged with the nvoi CLI version so backups are
// deterministic per deploy — same discipline as user workloads, which
// carry the deploy-hash as part of the image tag.
//
// The image is built from cmd/backup/Dockerfile and published to Docker
// Hub on every `v*` git tag. Public repo, no auth required for pull.
func BackupImage() string {
	return "docker.io/nvoi/backup:" + utils.Version
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
	labels := map[string]string{
		"app.kubernetes.io/name":       req.FullName,
		"app.kubernetes.io/managed-by": "nvoi",
		"nvoi/database":                req.Name,
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
								Image: BackupImage(),
								Env: []corev1.EnvVar{
									{Name: "ENGINE", Value: req.Spec.Engine},
									{Name: "DATABASE_NAME", Value: req.Name},
									{Name: "DATABASE_FULL_NAME", Value: req.FullName},
								},
								EnvFrom: []corev1.EnvFromSource{
									// The credentials Secret carries `url` (and
									// selfhosted: user/password/host/port/database);
									// the image reads DATABASE_URL from the `url` key.
									{
										Prefix: "DB_",
										SecretRef: &corev1.SecretEnvSource{
											LocalObjectReference: corev1.LocalObjectReference{Name: req.CredentialsSecretName},
										},
									},
									// Bucket creds: BUCKET_ENDPOINT / BUCKET_NAME /
									// AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY /
									// AWS_REGION. No prefix — sigv4 tooling reads
									// the AWS_* names directly.
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
