package core

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func init() {
	provider.RegisterCompute("cron-test", provider.CredentialSchema{Name: "cron-test"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{
				ID: "1", Name: "nvoi-myapp-prod-master", Status: "running",
				IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
			}},
			Volumes: []*provider.Volume{{
				Name: "nvoi-myapp-prod-pgdata",
			}},
		}
	})
}

func testCronCluster(kc *kube.Client) Cluster {
	return Cluster{
		AppName: "myapp", Env: "prod",
		Provider: "cron-test", Credentials: map[string]string{},
		Output:     &testutil.MockOutput{},
		MasterKube: kc,
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return nil, nil
		},
	}
}

func TestCronSet_AppliesCronJob(t *testing.T) {
	kc := testKube()
	err := CronSet(context.Background(), CronSetRequest{
		Cluster:  testCronCluster(kc),
		Name:     "backup",
		Image:    "busybox",
		Schedule: "0 1 * * *",
	})
	if err != nil {
		t.Fatalf("CronSet: %v", err)
	}
	got, err := kc.Clientset().BatchV1().CronJobs("nvoi-myapp-prod").Get(context.Background(), "backup", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("cronjob not applied: %v", err)
	}
	if got.Spec.Schedule != "0 1 * * *" {
		t.Errorf("schedule = %q", got.Spec.Schedule)
	}
}

func TestCronSet_SvcSecretsInManifest(t *testing.T) {
	kc := testKube()
	err := CronSet(context.Background(), CronSetRequest{
		Cluster:    testCronCluster(kc),
		Name:       "backup",
		Image:      "busybox",
		Schedule:   "0 1 * * *",
		SvcSecrets: []string{"STORAGE_BACKUPS_ENDPOINT", "STORAGE_BACKUPS_BUCKET"},
	})
	if err != nil {
		t.Fatalf("CronSet: %v", err)
	}
	cj, err := kc.Clientset().BatchV1().CronJobs("nvoi-myapp-prod").Get(context.Background(), "backup", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env
	if len(env) != 2 {
		t.Fatalf("env count = %d, want 2", len(env))
	}
	foundEndpoint := false
	for _, e := range env {
		if e.Name == "STORAGE_BACKUPS_ENDPOINT" && e.ValueFrom.SecretKeyRef.Name == "backup-secrets" {
			foundEndpoint = true
		}
	}
	if !foundEndpoint {
		t.Errorf("per-cron secret ref not wired: %+v", env)
	}
}

func TestCronSet_ResolvesNamedManagedVolumes(t *testing.T) {
	kc := testKube()
	err := CronSet(context.Background(), CronSetRequest{
		Cluster:  testCronCluster(kc),
		Name:     "backup",
		Image:    "busybox",
		Schedule: "0 1 * * *",
		Volumes:  []string{"pgdata:/data"},
	})
	if err != nil {
		t.Fatalf("CronSet: %v", err)
	}
	cj, err := kc.Clientset().BatchV1().CronJobs("nvoi-myapp-prod").Get(context.Background(), "backup", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	vols := cj.Spec.JobTemplate.Spec.Template.Spec.Volumes
	if len(vols) != 1 || vols[0].HostPath == nil {
		t.Fatalf("volumes = %+v", vols)
	}
	if vols[0].HostPath.Path != "/mnt/data/nvoi-myapp-prod-pgdata" {
		t.Errorf("hostPath = %q, want /mnt/data/nvoi-myapp-prod-pgdata", vols[0].HostPath.Path)
	}
}

// withJobStatus installs a reactor that mutates any created/fetched Job's
// status to the given values. Lets WaitForJob's first poll see the terminal
// state without needing a background goroutine.
func withJobStatus(kc *kube.Client, succeeded, failed int32) {
	cs := kc.Clientset().(*k8sfake.Clientset)
	cs.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		ca, ok := action.(k8stesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		j := ca.GetObject().(*batchv1.Job).DeepCopy()
		j.Status.Succeeded = succeeded
		j.Status.Failed = failed
		return false, j, nil // false → let the tracker also record it
	})
	cs.PrependReactor("get", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		ga, ok := action.(k8stesting.GetAction)
		if !ok {
			return false, nil, nil
		}
		got, err := cs.Tracker().Get(batchv1.SchemeGroupVersion.WithResource("jobs"), ga.GetNamespace(), ga.GetName())
		if err != nil {
			return false, nil, err
		}
		j := got.(*batchv1.Job).DeepCopy()
		j.Status.Succeeded = succeeded
		j.Status.Failed = failed
		return true, j, nil
	})
}

func TestCronRun_Success(t *testing.T) {
	existing := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "db-backup", Namespace: "nvoi-myapp-prod"},
		Spec: batchv1.CronJobSpec{
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "b", Image: "busybox"}},
						},
					},
				},
			},
		},
	}
	kc := testKube(existing)
	withJobStatus(kc, 1, 0)

	kube.SetTestTiming(time.Millisecond, time.Millisecond)

	err := CronRun(context.Background(), CronRunRequest{Cluster: testCronCluster(kc), Name: "db-backup"})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	jobs, _ := kc.Clientset().BatchV1().Jobs("nvoi-myapp-prod").List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) == 0 {
		t.Fatal("CronRun did not create a job")
	}
	if !strings.HasPrefix(jobs.Items[0].Name, "db-backup-run-") {
		t.Errorf("job name = %q, want prefix db-backup-run-", jobs.Items[0].Name)
	}
}

func TestCronRun_JobFailed(t *testing.T) {
	existing := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "db-backup", Namespace: "nvoi-myapp-prod"},
		Spec: batchv1.CronJobSpec{
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "b", Image: "busybox"}},
						},
					},
				},
			},
		},
	}
	kc := testKube(existing)
	withJobStatus(kc, 0, 1)

	kube.SetTestTiming(time.Millisecond, time.Millisecond)

	err := CronRun(context.Background(), CronRunRequest{Cluster: testCronCluster(kc), Name: "db-backup"})
	if err == nil {
		t.Fatal("expected error for failed job")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("error should mention failure, got: %v", err)
	}
}

func TestCronDelete_IdempotentWhenMissing(t *testing.T) {
	kc := testKube()
	err := CronDelete(context.Background(), CronDeleteRequest{
		Cluster: testCronCluster(kc),
		Name:    "missing",
	})
	if err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

func TestCronDelete_RemovesExisting(t *testing.T) {
	existing := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "nvoi-myapp-prod"},
	}
	kc := testKube(existing)

	err := CronDelete(context.Background(), CronDeleteRequest{
		Cluster: testCronCluster(kc),
		Name:    "backup",
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = kc.Clientset().BatchV1().CronJobs("nvoi-myapp-prod").Get(context.Background(), "backup", metav1.GetOptions{})
	if err == nil {
		t.Error("cronjob should be gone")
	}
}
