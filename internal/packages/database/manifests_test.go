package database

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── generateManifests tests ──────────────────────────────────────────────────

func TestGenerateManifests_Postgres_HostPath(t *testing.T) {
	// THE INVARIANT: the HostPath in the StatefulSet MUST equal the
	// resolved volume mount path passed to generateManifests.
	// This is the exact bug that existed before — the old code derived
	// its own path (name + "-db") instead of using the actual volume path.

	svcName := utils.DatabaseServiceName("main")
	secretName := utils.DatabaseSecretName("main")
	names, _ := utils.NewNames("myapp", "production")
	volumeMountPath := names.VolumeMountPath("pgdata") // the REAL volume name

	manifest := generateManifests("main", svcName, secretName, volumeMountPath,
		&Postgres{}, "postgres:17", "nvoi-myapp-production", "master")

	ss := parseStatefulSet(t, manifest)

	// HostPath must be the resolved volume path — NOT a derived path
	hostPath := ss.Spec.Template.Spec.Volumes[0].HostPath.Path
	if hostPath != volumeMountPath {
		t.Fatalf("INVARIANT VIOLATION:\n  HostPath       = %q\n  VolumeMountPath = %q\n  These MUST be identical. The HostPath must come from the resolved volume, not a derived name.",
			hostPath, volumeMountPath)
	}

	// Verify it's the ACTUAL volume path, not the old broken name+"-db" path
	brokenPath := names.VolumeMountPath("main-db")
	if hostPath == brokenPath {
		t.Fatalf("HostPath = %q matches the old broken convention (name+'-db'). Must use the actual volume name 'pgdata'.", hostPath)
	}
}

func TestGenerateManifests_Postgres_Structure(t *testing.T) {
	svcName := utils.DatabaseServiceName("main")
	secretName := utils.DatabaseSecretName("main")
	names, _ := utils.NewNames("myapp", "production")
	volumeMountPath := names.VolumeMountPath("pgdata")

	manifest := generateManifests("main", svcName, secretName, volumeMountPath,
		&Postgres{}, "postgres:17", "nvoi-myapp-production", "bugsink")

	ss := parseStatefulSet(t, manifest)
	svc := parseService(t, manifest)

	// StatefulSet name
	if ss.Name != "main-db" {
		t.Errorf("StatefulSet.Name = %q, want %q", ss.Name, "main-db")
	}
	if ss.Namespace != "nvoi-myapp-production" {
		t.Errorf("StatefulSet.Namespace = %q, want %q", ss.Namespace, "nvoi-myapp-production")
	}

	// NodeSelector — must target the correct server
	nodeSelector := ss.Spec.Template.Spec.NodeSelector
	if nodeSelector[utils.LabelNvoiRole] != "bugsink" {
		t.Errorf("NodeSelector[%q] = %q, want %q", utils.LabelNvoiRole, nodeSelector[utils.LabelNvoiRole], "bugsink")
	}

	// Container
	container := ss.Spec.Template.Spec.Containers[0]
	if container.Name != "main-db" {
		t.Errorf("Container.Name = %q, want %q", container.Name, "main-db")
	}
	if container.Image != "postgres:17" {
		t.Errorf("Container.Image = %q, want %q", container.Image, "postgres:17")
	}
	if container.Ports[0].ContainerPort != 5432 {
		t.Errorf("ContainerPort = %d, want 5432", container.Ports[0].ContainerPort)
	}

	// Container env references the correct secret
	assertEnvFromSecret(t, container.Env, "POSTGRES_USER", secretName, "MAIN_POSTGRES_USER")
	assertEnvFromSecret(t, container.Env, "POSTGRES_PASSWORD", secretName, "MAIN_POSTGRES_PASSWORD")
	assertEnvFromSecret(t, container.Env, "POSTGRES_DB", secretName, "MAIN_POSTGRES_DB")

	// Labels
	if ss.Labels[utils.LabelAppName] != "main-db" {
		t.Errorf("StatefulSet.Labels[%q] = %q, want %q", utils.LabelAppName, ss.Labels[utils.LabelAppName], "main-db")
	}

	// Service
	if svc.Name != "main-db" {
		t.Errorf("Service.Name = %q, want %q", svc.Name, "main-db")
	}
	if svc.Spec.ClusterIP != "None" {
		t.Errorf("Service.ClusterIP = %q, want %q", svc.Spec.ClusterIP, "None")
	}
}

func TestGenerateManifests_MySQL_Structure(t *testing.T) {
	svcName := utils.DatabaseServiceName("analytics")
	secretName := utils.DatabaseSecretName("analytics")
	names, _ := utils.NewNames("myapp", "production")
	volumeMountPath := names.VolumeMountPath("mysqldata")

	manifest := generateManifests("analytics", svcName, secretName, volumeMountPath,
		&MySQL{}, "mysql:8", "nvoi-myapp-production", "worker")

	ss := parseStatefulSet(t, manifest)

	// HostPath must be the resolved volume path
	hostPath := ss.Spec.Template.Spec.Volumes[0].HostPath.Path
	if hostPath != volumeMountPath {
		t.Fatalf("INVARIANT VIOLATION: HostPath = %q, VolumeMountPath = %q — must be identical",
			hostPath, volumeMountPath)
	}

	// MySQL-specific
	if ss.Name != "analytics-db" {
		t.Errorf("StatefulSet.Name = %q, want %q", ss.Name, "analytics-db")
	}
	container := ss.Spec.Template.Spec.Containers[0]
	if container.Ports[0].ContainerPort != 3306 {
		t.Errorf("ContainerPort = %d, want 3306", container.Ports[0].ContainerPort)
	}

	// MySQL env references correct secret
	assertEnvFromSecret(t, container.Env, "MYSQL_USER", secretName, "ANALYTICS_MYSQL_USER")
	assertEnvFromSecret(t, container.Env, "MYSQL_PASSWORD", secretName, "ANALYTICS_MYSQL_PASSWORD")
	assertEnvFromSecret(t, container.Env, "MYSQL_DATABASE", secretName, "ANALYTICS_MYSQL_DATABASE")
}

func TestGenerateManifests_HostPathIsExactInput(t *testing.T) {
	// The HostPath must be EXACTLY the volumeMountPath argument.
	// No transformation, no derivation, no suffix, no prefix.
	// Whatever value is passed in is what appears in the YAML.
	arbitraryPath := "/some/completely/arbitrary/path"

	manifest := generateManifests("x", "x-db", "x-db-credentials", arbitraryPath,
		&Postgres{}, "postgres:17", "ns", "srv")

	ss := parseStatefulSet(t, manifest)
	hostPath := ss.Spec.Template.Spec.Volumes[0].HostPath.Path
	if hostPath != arbitraryPath {
		t.Fatalf("HostPath = %q, want exact input %q — function must not derive or transform the path",
			hostPath, arbitraryPath)
	}
}

// ── generateBackupCronJob tests ─────────────────────────────────────────────

func TestGenerateBackupCronJob_Structure(t *testing.T) {
	cronName := utils.DatabaseBackupCronName("main")
	dbSecretName := utils.DatabaseSecretName("main")
	names, _ := utils.NewNames("myapp", "production")
	svcName := utils.DatabaseServiceName("main")
	bucketName := utils.DatabaseBackupBucket("main")

	manifest := generateBackupCronJob("main", cronName, dbSecretName,
		&Postgres{}, "postgres:17", "nvoi-myapp-production", names,
		svcName, "0 */6 * * *", 7, bucketName)

	cj := parseCronJob(t, manifest)

	// CronJob name
	if cj.Name != "main-db-backup" {
		t.Errorf("CronJob.Name = %q, want %q", cj.Name, "main-db-backup")
	}

	// Schedule
	if cj.Spec.Schedule != "0 */6 * * *" {
		t.Errorf("Schedule = %q, want %q", cj.Spec.Schedule, "0 */6 * * *")
	}

	// Container references correct secrets
	container := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
	if container.Name != "main-db-backup" {
		t.Errorf("Container.Name = %q, want %q", container.Name, "main-db-backup")
	}

	// DB credential env vars reference the correct secret
	assertEnvFromSecret(t, container.Env, "DB_USER", dbSecretName, "MAIN_POSTGRES_USER")
	assertEnvFromSecret(t, container.Env, "DB_PASSWORD", dbSecretName, "MAIN_POSTGRES_PASSWORD")
	assertEnvFromSecret(t, container.Env, "DB_NAME", dbSecretName, "MAIN_POSTGRES_DB")

	// Bucket credential env vars reference the per-cron secret
	bucketSecretName := names.KubeServiceSecrets(cronName) // main-db-backup-secrets
	assertEnvFromSecret(t, container.Env, "STORAGE_ENDPOINT", bucketSecretName, "STORAGE_MAIN_DB_BACKUPS_ENDPOINT")
	assertEnvFromSecret(t, container.Env, "STORAGE_BUCKET", bucketSecretName, "STORAGE_MAIN_DB_BACKUPS_BUCKET")

	// Labels
	if cj.Labels[utils.LabelAppName] != "main-db-backup" {
		t.Errorf("CronJob.Labels[%q] = %q, want %q", utils.LabelAppName, cj.Labels[utils.LabelAppName], "main-db-backup")
	}
}

// ── Naming consistency tests ────────────────────────────────────────────────

func TestNamingConventions_AllDeriveFromSameSource(t *testing.T) {
	// All database resource names must come from utils.Database* functions.
	// This test verifies the functions produce consistent, predictable names.
	dbName := "analytics"

	service := utils.DatabaseServiceName(dbName)
	secret := utils.DatabaseSecretName(dbName)
	backupCron := utils.DatabaseBackupCronName(dbName)
	backupBucket := utils.DatabaseBackupBucket(dbName)
	pod := utils.DatabasePodName(dbName)

	if service != "analytics-db" {
		t.Errorf("DatabaseServiceName = %q", service)
	}
	if secret != "analytics-db-credentials" {
		t.Errorf("DatabaseSecretName = %q", secret)
	}
	if backupCron != "analytics-db-backup" {
		t.Errorf("DatabaseBackupCronName = %q", backupCron)
	}
	if backupBucket != "analytics-db-backups" {
		t.Errorf("DatabaseBackupBucket = %q", backupBucket)
	}
	if pod != "analytics-db-0" {
		t.Errorf("DatabasePodName = %q", pod)
	}

	// Pod name must be service name + "-0" (k8s StatefulSet convention)
	if pod != service+"-0" {
		t.Errorf("DatabasePodName(%q) = %q, but DatabaseServiceName = %q — pod must be service+'-0'",
			dbName, pod, service)
	}
}

// ── Test helpers ────────────────────────────────────────────────────────────

func parseStatefulSet(t *testing.T, manifest string) appsv1.StatefulSet {
	t.Helper()
	docs := strings.Split(manifest, "---")
	var ss appsv1.StatefulSet
	if err := sigsyaml.Unmarshal([]byte(docs[0]), &ss); err != nil {
		t.Fatalf("failed to parse StatefulSet YAML: %v", err)
	}
	if ss.Kind != "StatefulSet" {
		t.Fatalf("first document is %q, expected StatefulSet", ss.Kind)
	}
	return ss
}

func parseService(t *testing.T, manifest string) corev1.Service {
	t.Helper()
	docs := strings.Split(manifest, "---")
	if len(docs) < 2 {
		t.Fatal("manifest has no second document (Service)")
	}
	var svc corev1.Service
	if err := sigsyaml.Unmarshal([]byte(docs[1]), &svc); err != nil {
		t.Fatalf("failed to parse Service YAML: %v", err)
	}
	return svc
}

func parseCronJob(t *testing.T, manifest string) batchv1.CronJob {
	t.Helper()
	var cj batchv1.CronJob
	if err := sigsyaml.Unmarshal([]byte(manifest), &cj); err != nil {
		t.Fatalf("failed to parse CronJob YAML: %v", err)
	}
	return cj
}

func assertEnvFromSecret(t *testing.T, envVars []corev1.EnvVar, envName, wantSecret, wantKey string) {
	t.Helper()
	for _, ev := range envVars {
		if ev.Name == envName {
			if ev.ValueFrom == nil || ev.ValueFrom.SecretKeyRef == nil {
				t.Errorf("env %q has no secretKeyRef", envName)
				return
			}
			ref := ev.ValueFrom.SecretKeyRef
			if ref.Name != wantSecret {
				t.Errorf("env %q references secret %q, want %q", envName, ref.Name, wantSecret)
			}
			if ref.Key != wantKey {
				t.Errorf("env %q secret key = %q, want %q", envName, ref.Key, wantKey)
			}
			return
		}
	}
	t.Errorf("env %q not found in container env vars", envName)
}
