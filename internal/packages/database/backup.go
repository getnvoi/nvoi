package database

import (
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func generateBackupCronJob(name, image, ns string, names *utils.Names, dbSvcName, schedule string, retain int, bucketName string) string {
	cronName := name + "-db-backup"
	dbSecretName := name + "-db-credentials"
	bucketSecretName := names.KubeSecrets()
	prefix := strings.ToUpper(name)
	storagePrefix := strings.ToUpper(bucketName)
	storagePrefix = strings.ReplaceAll(storagePrefix, "-", "_")

	labels := map[string]string{
		utils.LabelAppName:      cronName,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
	}

	script := fmt.Sprintf(`set -e
export AWS_ACCESS_KEY_ID=$STORAGE_ACCESS_KEY_ID
export AWS_SECRET_ACCESS_KEY=$STORAGE_SECRET_ACCESS_KEY
apt-get update -qq && apt-get install -y -qq curl unzip > /dev/null
ARCH=$(dpkg --print-architecture)
if [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then
  curl -sL https://awscli.amazonaws.com/awscli-exe-linux-aarch64.zip -o /tmp/aws.zip
else
  curl -sL https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip -o /tmp/aws.zip
fi
unzip -q /tmp/aws.zip -d /tmp && /tmp/aws/install > /dev/null
TIMESTAMP=$(date +%%Y%%m%%d-%%H%%M%%S)
PGPASSWORD=$POSTGRES_PASSWORD pg_dump -h %s -U $POSTGRES_USER -d $POSTGRES_DB --no-owner --no-acl | \
gzip | \
aws s3 cp - "s3://$STORAGE_BUCKET/backups/backup-$TIMESTAMP.sql.gz" --endpoint-url "$STORAGE_ENDPOINT"
aws s3 ls "s3://$STORAGE_BUCKET/backups/" --endpoint-url "$STORAGE_ENDPOINT" | \
sort -r | tail -n +%d | awk '{print $4}' | \
xargs -I {} aws s3 rm "s3://$STORAGE_BUCKET/backups/{}" --endpoint-url "$STORAGE_ENDPOINT"
`, dbSvcName, retain+1)

	one := int32(1)
	zero := int32(0)
	concurrencyForbid := batchv1.ForbidConcurrent

	job := batchv1.CronJob{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "CronJob"},
		ObjectMeta: metav1.ObjectMeta{Name: cronName, Namespace: ns, Labels: labels},
		Spec: batchv1.CronJobSpec{
			Schedule:                   schedule,
			ConcurrencyPolicy:          concurrencyForbid,
			SuccessfulJobsHistoryLimit: &one,
			FailedJobsHistoryLimit:     &one,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					BackoffLimit: &zero,
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							NodeSelector:  map[string]string{utils.LabelNvoiRole: utils.RoleMaster},
							Containers: []corev1.Container{{
								Name:    cronName,
								Image:   backupImage(image),
								Command: []string{"/bin/bash", "-c"},
								Args:    []string{script},
								Env: []corev1.EnvVar{
									secretEnv("POSTGRES_USER", dbSecretName, prefix+"_POSTGRES_USER"),
									secretEnv("POSTGRES_PASSWORD", dbSecretName, prefix+"_POSTGRES_PASSWORD"),
									secretEnv("POSTGRES_DB", dbSecretName, prefix+"_POSTGRES_DB"),
									secretEnv("STORAGE_ENDPOINT", bucketSecretName, "STORAGE_"+storagePrefix+"_ENDPOINT"),
									secretEnv("STORAGE_BUCKET", bucketSecretName, "STORAGE_"+storagePrefix+"_BUCKET"),
									secretEnv("STORAGE_ACCESS_KEY_ID", bucketSecretName, "STORAGE_"+storagePrefix+"_ACCESS_KEY_ID"),
									secretEnv("STORAGE_SECRET_ACCESS_KEY", bucketSecretName, "STORAGE_"+storagePrefix+"_SECRET_ACCESS_KEY"),
								},
							}},
						},
					},
				},
			},
		},
	}

	b, _ := sigsyaml.Marshal(job)
	return strings.TrimSpace(string(b))
}

// backupImage returns an image with awscli pre-installed.
// For now, uses the same postgres image — awscli installed at runtime.
// Future: embedded Dockerfile with awscli baked in.
func backupImage(dbImage string) string {
	return dbImage
}
