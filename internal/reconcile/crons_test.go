package reconcile

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestCrons_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Crons:   map[string]config.CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
	}

	if err := Crons(context.Background(), dc, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !kfFor(dc).HasCronJob(testNS, "cleanup") {
		t.Error("cleanup CronJob not applied")
	}
}

func TestCrons_OrphanRemoved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	kf := kfFor(dc)

	// Pre-populate orphan CronJob (nvoi labels for orphan listing).
	_, err := kf.Typed.BatchV1().CronJobs(testNS).Create(context.Background(),
		&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{
			Name: "old-job", Namespace: testNS,
			Labels: map[string]string{utils.LabelAppManagedBy: utils.LabelManagedBy},
		}},
		metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Crons:   map[string]config.CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
	}

	if err := Crons(context.Background(), dc, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kf.HasCronJob(testNS, "old-job") {
		t.Error("orphan old-job should have been deleted")
	}
	if !kf.HasCronJob(testNS, "cleanup") {
		t.Error("desired cleanup should exist")
	}
}

func TestCrons_AlreadyConverged(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	kf := kfFor(dc)

	_, err := kf.Typed.BatchV1().CronJobs(testNS).Create(context.Background(),
		&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Name: "cleanup", Namespace: testNS},
			Spec:       batchv1.CronJobSpec{Schedule: "0 * * * *"},
		},
		metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Crons:   map[string]config.CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
	}

	if err := Crons(context.Background(), dc, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !kf.HasCronJob(testNS, "cleanup") {
		t.Error("converged cron should not be deleted")
	}
}
