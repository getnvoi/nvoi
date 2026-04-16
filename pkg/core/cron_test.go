package core

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/testutil"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"k8s.io/client-go/kubernetes/fake"
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

func testCronCluster() Cluster {
	return Cluster{
		AppName: "myapp", Env: "prod",
		Provider: "cron-test", Credentials: map[string]string{},
		Kube:     kube.NewFromClientset(fake.NewSimpleClientset()),
		MasterIP: "1.2.3.4",
	}
}

func TestCronSet_SvcSecretsInManifest(t *testing.T) {
	err := CronSet(context.Background(), CronSetRequest{
		Cluster:    testCronCluster(),
		Name:       "backup",
		Image:      "busybox",
		Schedule:   "0 1 * * *",
		SvcSecrets: []string{"STORAGE_BACKUPS_ENDPOINT", "STORAGE_BACKUPS_BUCKET"},
	})
	if err != nil {
		t.Fatalf("CronSet: %v", err)
	}
}

func TestCronSet_ResolvesNamedManagedVolumes(t *testing.T) {
	err := CronSet(context.Background(), CronSetRequest{
		Cluster:  testCronCluster(),
		Name:     "backup",
		Image:    "busybox",
		Schedule: "0 1 * * *",
		Volumes:  []string{"pgdata:/data"},
	})
	if err != nil {
		t.Fatalf("CronSet: %v", err)
	}
}

func TestCronRun_Success(t *testing.T) {
	ns := "nvoi-myapp-prod"
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "db-backup", Namespace: ns},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 */6 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers:    []corev1.Container{{Name: "backup", Image: "busybox"}},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
			},
		},
	}

	cs := fake.NewSimpleClientset(cronJob)
	// Reactor: when a Job is created, immediately mark it as succeeded.
	cs.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction := action.(k8stesting.CreateAction)
		job := createAction.GetObject().(*batchv1.Job)
		job.Status.Succeeded = 1
		job.Status.Conditions = []batchv1.JobCondition{
			{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
		}
		return false, job, nil // false = let the fake store it too
	})

	c := testCronCluster()
	c.Kube = kube.NewFromClientset(cs)

	err := CronRun(context.Background(), CronRunRequest{Cluster: c, Name: "db-backup"})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestCronRun_JobFailed(t *testing.T) {
	ns := "nvoi-myapp-prod"
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "db-backup", Namespace: ns},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 */6 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers:    []corev1.Container{{Name: "backup", Image: "busybox"}},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
			},
		},
	}

	cs := fake.NewSimpleClientset(cronJob)
	cs.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction := action.(k8stesting.CreateAction)
		job := createAction.GetObject().(*batchv1.Job)
		job.Status.Failed = 1
		job.Status.Conditions = []batchv1.JobCondition{
			{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Message: "pg_dump: connection refused"},
		}
		return false, job, nil
	})

	c := testCronCluster()
	c.Kube = kube.NewFromClientset(cs)

	err := CronRun(context.Background(), CronRunRequest{Cluster: c, Name: "db-backup"})
	if err == nil {
		t.Fatal("expected error for failed job")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("error should mention failure, got: %v", err)
	}
}

func TestCronDelete_IdempotentWhenMissing(t *testing.T) {
	err := CronDelete(context.Background(), CronDeleteRequest{
		Cluster: testCronCluster(),
		Name:    "backup",
	})
	if err != nil {
		t.Fatalf("CronDelete: %v", err)
	}
}
