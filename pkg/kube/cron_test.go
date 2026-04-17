package kube

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestBuildCronJob_Basic(t *testing.T) {
	cj, err := BuildCronJob(CronSpec{
		Name:     "backup",
		Schedule: "0 1 * * *",
		Image:    "busybox:latest",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cj.Spec.Schedule != "0 1 * * *" {
		t.Errorf("schedule = %q", cj.Spec.Schedule)
	}
	containers := cj.Spec.JobTemplate.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Image != "busybox:latest" {
		t.Errorf("container = %+v", containers)
	}
	if cj.Labels[utils.LabelAppName] != "backup" {
		t.Errorf("labels missing app name: %v", cj.Labels)
	}
}

func TestBuildCronJob_CommandWrapping(t *testing.T) {
	cj, err := BuildCronJob(CronSpec{
		Name:     "cleanup",
		Schedule: "0 * * * *",
		Image:    "busybox",
		Command:  "echo hi",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ct := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
	if len(ct.Command) != 2 || ct.Command[0] != "/bin/sh" || ct.Command[1] != "-c" {
		t.Errorf("command = %v", ct.Command)
	}
	if len(ct.Args) != 1 || ct.Args[0] != "echo hi" {
		t.Errorf("args = %v", ct.Args)
	}
}

func TestBuildCronJob_SecretRef(t *testing.T) {
	cj, err := BuildCronJob(CronSpec{
		Name:          "bg",
		Schedule:      "0 * * * *",
		Image:         "busybox",
		SvcSecrets:    []string{"API_KEY"},
		SvcSecretName: "bg-secrets",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	envs := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env
	if len(envs) != 1 {
		t.Fatalf("env count = %d, want 1", len(envs))
	}
	kr := envs[0].ValueFrom.SecretKeyRef
	if kr == nil || kr.Name != "bg-secrets" || kr.Key != "API_KEY" || envs[0].Name != "API_KEY" {
		t.Errorf("env = %+v", envs[0])
	}
}

func TestBuildCronJob_AliasedSecretRef(t *testing.T) {
	cj, err := BuildCronJob(CronSpec{
		Name:          "bg",
		Schedule:      "0 * * * *",
		Image:         "busybox",
		SvcSecrets:    []string{"DB_URL=MAIN_DATABASE_URL"},
		SvcSecretName: "bg-secrets",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	envs := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env
	if envs[0].Name != "DB_URL" || envs[0].ValueFrom.SecretKeyRef.Key != "MAIN_DATABASE_URL" {
		t.Errorf("aliased secret wired wrong: %+v", envs[0])
	}
}

func TestBuildCronJob_NodeSelector_SingleServer(t *testing.T) {
	cj, err := BuildCronJob(CronSpec{
		Name:     "nightly",
		Schedule: "0 3 * * *",
		Image:    "busybox",
		Servers:  []string{"worker-1"},
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	sel := cj.Spec.JobTemplate.Spec.Template.Spec.NodeSelector
	if sel[utils.LabelNvoiRole] != "worker-1" {
		t.Errorf("nodeSelector = %v", sel)
	}
}

func TestDeleteCronByName_Idempotent(t *testing.T) {
	c := newTestClient()
	if err := c.DeleteCronByName(context.Background(), "ns", "gone"); err != nil {
		t.Errorf("absent cronjob must not error: %v", err)
	}
	existing := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "here", Namespace: "ns"},
	}
	c = newTestClient(existing)
	if err := c.DeleteCronByName(context.Background(), "ns", "here"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.cs.BatchV1().CronJobs("ns").Get(context.Background(), "here", metav1.GetOptions{}); err == nil {
		t.Error("cronjob should be gone")
	}
}

func TestCreateJobFromCronJob_CopiesTemplate(t *testing.T) {
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "ns", UID: "abc"},
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
	c := newTestClient(cj)

	if err := c.CreateJobFromCronJob(context.Background(), "ns", "backup", "backup-manual"); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := c.cs.BatchV1().Jobs("ns").Get(context.Background(), "backup-manual", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Annotations["cronjob.kubernetes.io/instantiate"] != "manual" {
		t.Error("manual annotation missing")
	}
	if len(got.OwnerReferences) != 1 || got.OwnerReferences[0].UID != "abc" {
		t.Errorf("owner refs = %+v", got.OwnerReferences)
	}
	if got.Spec.Template.Spec.Containers[0].Image != "busybox" {
		t.Error("template not copied from cronjob")
	}
}
