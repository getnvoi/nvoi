package postgres

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

type Provider struct{}

func (p *Provider) ValidateCredentials(context.Context) error { return nil }
func (p *Provider) Close() error                              { return nil }
func (p *Provider) ListResources(context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}

func (p *Provider) EnsureCredentials(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest) (provider.DatabaseCredentials, error) {
	creds := credentials(req)
	if kc != nil {
		if err := kc.EnsureSecret(ctx, req.Namespace, req.CredentialsSecretName, map[string]string{
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

func (p *Provider) Reconcile(_ context.Context, req provider.DatabaseRequest) (*provider.DatabasePlan, error) {
	workloads := []runtime.Object{
		buildService(req),
		buildPVC(req),
		buildStatefulSet(req),
	}
	if req.Spec.Backup != nil && req.Spec.Backup.Schedule != "" {
		workloads = append(workloads, buildBackupCron(req))
	}
	return &provider.DatabasePlan{Workloads: workloads}, nil
}

func (p *Provider) Delete(context.Context, provider.DatabaseRequest) error { return nil }

func (p *Provider) ExecSQL(ctx context.Context, req provider.DatabaseRequest, stmt string) (*provider.SQLResult, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("postgres.ExecSQL requires kube client")
	}
	return ExecSQLWithKube(ctx, req.Kube, req, stmt)
}

func (p *Provider) BackupNow(context.Context, provider.DatabaseRequest) (*provider.BackupRef, error) {
	return nil, fmt.Errorf("postgres backup now is not implemented yet")
}

func (p *Provider) ListBackups(context.Context, provider.DatabaseRequest) ([]provider.BackupRef, error) {
	return nil, fmt.Errorf("postgres backup list is not implemented yet")
}

func (p *Provider) DownloadBackup(context.Context, provider.DatabaseRequest, string, io.Writer) error {
	return fmt.Errorf("postgres backup download is not implemented yet")
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

func labels(req provider.DatabaseRequest) map[string]string {
	labels := map[string]string{}
	for k, v := range req.Labels {
		labels[k] = v
	}
	labels["nvoi/database"] = req.Name
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
	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.PVCName,
			Namespace: req.Namespace,
			Labels:    labels(req),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
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
	version := req.Spec.Version
	if version == "" {
		version = "16"
	}
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
						Image: "postgres:" + version + "-alpine",
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

func buildBackupCron(req provider.DatabaseRequest) runtime.Object {
	return &batchv1.CronJob{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "CronJob"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.BackupName,
			Namespace: req.Namespace,
			Labels:    labels(req),
		},
		Spec: batchv1.CronJobSpec{
			Schedule: req.Spec.Backup.Schedule,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{{
								Name:    "backup",
								Image:   "postgres:16-alpine",
								Command: []string{"sh", "-lc", "echo backup placeholder"},
							}},
						},
					},
				},
			},
		},
	}
}
