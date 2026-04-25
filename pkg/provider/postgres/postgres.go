package postgres

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// defaultPostgresVersion is the fallback tag when cfg omits a
// databases.X.version: value. Keep aligned with the latest "current"
// stable postgres major; bumping here is a deliberate commit.
const defaultPostgresVersion = "16"

// ImageFor returns the container image reference for a postgres pod
// given a user-declared version string (from databases.X.version: in
// cfg). Empty → default. Always the -alpine flavor to keep image
// pulls small on the DB node.
//
// Single source of truth for postgres image naming — buildStatefulSet
// AND the branch StatefulSet builder both call it so version bumps
// propagate to both the primary and every branch in one place.
func ImageFor(version string) string {
	if version == "" {
		version = defaultPostgresVersion
	}
	return "postgres:" + version + "-alpine"
}

type Provider struct{}

func (p *Provider) ValidateCredentials(context.Context) error { return nil }
func (p *Provider) Close() error                              { return nil }
func (p *Provider) ListResources(context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}

func (p *Provider) EnsureCredentials(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest) (provider.DatabaseCredentials, error) {
	creds := credentials(req)
	if kc != nil {
		if err := kc.EnsureSecret(ctx, req.Namespace, utils.OwnerDatabases, req.CredentialsSecretName, map[string]string{
			"url":      creds.URL,
			"host":     creds.Host,
			"port":     strconv.Itoa(creds.Port),
			"user":     creds.User,
			"password": creds.Password,
			"database": creds.Database,
			"sslmode":  creds.SSLMode,
		}); err != nil {
			return provider.DatabaseCredentials{}, err
		}
	}
	return creds, nil
}

func (p *Provider) Reconcile(ctx context.Context, req provider.DatabaseRequest) (*provider.DatabasePlan, error) {
	// ZFS prepare-node runs over SSH before we emit cluster workloads.
	// Installs zfsutils-linux + creates the file-backed zpool on the
	// DB's target node. Idempotent — re-runs are near-free. Skipped
	// when NodeSSH is nil (tests that don't wire SSH, or callers that
	// bootstrapped the node out-of-band).
	if req.NodeSSH != nil && req.Spec.Size > 0 {
		if err := PrepareNode(ctx, req.NodeSSH, req.Spec.Size); err != nil {
			return nil, fmt.Errorf("postgres.PrepareNode: %w", err)
		}
	}

	workloads := []runtime.Object{
		// StorageClass first so the PVC binds against it. Cluster-scoped,
		// idempotent Apply via the typed clientset.
		buildZFSStorageClass(),
		buildService(req),
		buildPVC(req),
		buildStatefulSet(req),
	}
	// Backup CronJob is the uniform path — every DatabaseProvider whose
	// Spec.Backup is set delegates to provider.BuildBackupCronJob. The
	// reconciler upstream is responsible for provisioning the bucket and
	// materializing the `-backup-creds` Secret this CronJob envFroms;
	// we just emit the workload.
	if req.Spec.Backup != nil && req.Spec.Backup.Schedule != "" {
		workloads = append(workloads, provider.BuildBackupCronJob(req))
	}
	return &provider.DatabasePlan{Workloads: workloads}, nil
}

// Delete is a no-op for postgres selfhosted — the StatefulSet /
// Service / PVC / Secret / CronJob die with the cluster namespace on
// teardown (or stay on re-deploy drift). The PVC lives under
// `--delete-volumes`, orthogonal to the DatabaseProvider contract.
func (p *Provider) Delete(context.Context, provider.DatabaseRequest) error { return nil }

func (p *Provider) ExecSQL(ctx context.Context, req provider.DatabaseRequest, stmt string) (*provider.SQLResult, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("postgres.ExecSQL requires kube client")
	}
	return ExecSQLWithKube(ctx, req.Kube, req, stmt)
}

// ListBackups / DownloadBackup delegate to the shared bucket helpers —
// every DatabaseProvider produces the same artifact (gzipped logical
// dump, prefix-free layout), so enumeration/download is engine-agnostic.
func (p *Provider) ListBackups(ctx context.Context, req provider.DatabaseRequest) ([]provider.BackupRef, error) {
	return provider.BucketListBackups(ctx, req)
}

func (p *Provider) DownloadBackup(ctx context.Context, req provider.DatabaseRequest, backupID string, w io.Writer) error {
	return provider.BucketDownloadBackup(ctx, req, backupID, w)
}

// Restore delegates to the uniform Job-based substrate shared by every
// DatabaseProvider. The `nvoi/db` image in MODE=restore pulls the
// artifact by key, gunzips, and pipes into `psql $DATABASE_URL` — the
// same DSN this provider's EnsureCredentials wrote into the credentials
// Secret. Postgres has no engine-specific restore logic here: the
// engine-specificity lives in the image's dispatch (cmd/db), same
// as backup.
func (p *Provider) Restore(ctx context.Context, req provider.DatabaseRequest, backupKey string) error {
	return provider.RunRestoreJob(ctx, req.Kube, req, backupKey)
}

func ExecSQLWithKube(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest, stmt string) (*provider.SQLResult, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := kc.Exec(ctx, kube.ExecRequest{
		Namespace: req.Namespace,
		Pod:       req.PodName,
		Command: []string{
			"psql",
			credentials(req).URL,
			"--csv",
			"-c",
			stmt,
		},
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return parseCSV(stdout.Bytes())
}

func parseCSV(b []byte) (*provider.SQLResult, error) {
	r := csv.NewReader(bytes.NewReader(b))
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	res := &provider.SQLResult{}
	if len(rows) == 0 {
		return res, nil
	}
	res.Columns = rows[0]
	if len(rows) > 1 {
		res.Rows = rows[1:]
		res.RowsAffected = int64(len(rows) - 1)
	}
	return res, nil
}

func credentials(req provider.DatabaseRequest) provider.DatabaseCredentials {
	host := req.FullName
	return provider.DatabaseCredentials{
		URL:      fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", req.Spec.User, req.Spec.Password, host, req.Spec.Database),
		Host:     host,
		Port:     5432,
		User:     req.Spec.User,
		Password: req.Spec.Password,
		Database: req.Spec.Database,
		SSLMode:  "disable",
	}
}

// labels returns the kube label set stamped on every postgres-owned
// k8s object — passes through the caller-supplied req.Labels
// (managed-by + app + env from names.KubeLabels). Ownership
// (nvoi/owner=databases) is added at apply time by
// kube.Client.ApplyOwned, NOT here, so the apply boundary stays the
// single source of truth for ownership.
func labels(req provider.DatabaseRequest) map[string]string {
	labels := map[string]string{}
	for k, v := range req.Labels {
		labels[k] = v
	}
	return labels
}

func buildService(req provider.DatabaseRequest) runtime.Object {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.FullName,
			Namespace: req.Namespace,
			Labels:    labels(req),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app.kubernetes.io/name": req.FullName},
			Ports: []corev1.ServicePort{{
				Name:       "postgres",
				Port:       5432,
				TargetPort: intstr.FromInt(5432),
			}},
		},
	}
}

func buildPVC(req provider.DatabaseRequest) runtime.Object {
	sc := ZFSStorageClassName
	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.PVCName,
			Namespace: req.Namespace,
			Labels:    labels(req),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			// Pinned to ZFS-LocalPV. size: → quota: on the dataset,
			// enforced at the block layer by the CSI driver. Without an
			// explicit StorageClassName the default SC (k3s local-path)
			// would silently win and size: would revert to advisory.
			StorageClassName: &sc,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", req.Spec.Size)),
				},
			},
		},
	}
}

func buildStatefulSet(req provider.DatabaseRequest) runtime.Object {
	replicas := int32(1)
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.FullName,
			Namespace: req.Namespace,
			Labels:    labels(req),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: req.FullName,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/name": req.FullName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app.kubernetes.io/name": req.FullName},
				},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{"nvoi-role": req.Spec.Server},
					Containers: []corev1.Container{{
						Name:  "postgres",
						Image: ImageFor(req.Spec.Version),
						Env: []corev1.EnvVar{
							{Name: "POSTGRES_USER", Value: req.Spec.User},
							{Name: "POSTGRES_PASSWORD", Value: req.Spec.Password},
							{Name: "POSTGRES_DB", Value: req.Spec.Database},
						},
						Ports: []corev1.ContainerPort{{ContainerPort: 5432, Name: "postgres"}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							MountPath: "/var/lib/postgresql/data",
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: req.PVCName},
						},
					}},
				},
			},
		},
	}
}

// BackupNow kicks a one-shot Job from the scheduled CronJob's template.
// The CronJob IS the source of truth for backup configuration (image,
// envFrom, entrypoint) — BackupNow just instantiates it out-of-band so
// `nvoi database backup now` doesn't drift from the scheduled path.
func BackupNowWithKube(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest) (*provider.BackupRef, error) {
	if req.Spec.Backup == nil || req.Spec.Backup.Schedule == "" {
		return nil, fmt.Errorf("postgres backup schedule is not configured")
	}
	jobName := fmt.Sprintf("%s-manual-%d", req.BackupName, time.Now().Unix())
	if err := kc.CreateJobFromCronJob(ctx, req.Namespace, req.BackupName, jobName); err != nil {
		return nil, err
	}
	return &provider.BackupRef{ID: jobName, CreatedAt: time.Now().UTC().Format(time.RFC3339), Kind: "dump"}, nil
}

func (p *Provider) BackupNow(ctx context.Context, req provider.DatabaseRequest) (*provider.BackupRef, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("postgres BackupNow requires kube client")
	}
	return BackupNowWithKube(ctx, req.Kube, req)
}
