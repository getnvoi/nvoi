package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestFirstPod_FirstMatchWins(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-abc",
			Namespace: "ns",
			Labels:    map[string]string{utils.LabelAppName: "web"},
		},
	}
	c := newTestClient(pod)

	got, err := c.FirstPod(context.Background(), "ns", "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "web-abc" {
		t.Errorf("got %q, want web-abc", got)
	}
}

func TestFirstPod_NoMatch(t *testing.T) {
	c := newTestClient()
	_, err := c.FirstPod(context.Background(), "ns", "web")
	if err == nil {
		t.Fatal("expected error for missing pod")
	}
	if !contains(err.Error(), "no pods found") {
		t.Errorf("error = %q, want 'no pods found'", err.Error())
	}
}

func TestGetServicePort_FirstPort(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 3000}, {Port: 9090}},
		},
	}
	c := newTestClient(svc)

	port, err := c.GetServicePort(context.Background(), "ns", "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 3000 {
		t.Errorf("port = %d, want 3000", port)
	}
}

func TestGetServicePort_NotFound(t *testing.T) {
	c := newTestClient()
	_, err := c.GetServicePort(context.Background(), "ns", "missing")
	if err == nil || !contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}
}

func TestGetServicePort_NoPorts(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
	}
	c := newTestClient(svc)

	_, err := c.GetServicePort(context.Background(), "ns", "web")
	if err == nil || !contains(err.Error(), "no port") {
		t.Fatalf("expected no-port error, got: %v", err)
	}
}

func TestDeleteByName_DeploymentAndService(t *testing.T) {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"}}
	c := newTestClient(dep, svc)

	if err := c.DeleteByName(context.Background(), "ns", "web"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := c.cs.AppsV1().Deployments("ns").Get(context.Background(), "web", metav1.GetOptions{}); err == nil {
		t.Error("deployment should be gone")
	}
	if _, err := c.cs.CoreV1().Services("ns").Get(context.Background(), "web", metav1.GetOptions{}); err == nil {
		t.Error("service should be gone")
	}
}

func TestDeleteByName_StatefulSet(t *testing.T) {
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"}}
	c := newTestClient(ss)

	if err := c.DeleteByName(context.Background(), "ns", "db"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := c.cs.AppsV1().StatefulSets("ns").Get(context.Background(), "db", metav1.GetOptions{}); err == nil {
		t.Error("statefulset should be gone")
	}
}

func TestDeleteByName_AllAbsent_Idempotent(t *testing.T) {
	c := newTestClient()
	if err := c.DeleteByName(context.Background(), "ns", "web"); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

// TestListWorkloadNames_ExcludesDatabaseOwned locks the orphan-sweep
// safety: DB-owned workloads (carrying utils.LabelNvoiDatabase) MUST
// NOT appear in the list reconcile.Services iterates for orphan
// deletion. Without this exclusion, a config with no `databases:` X →
// no `services: db` X means desired = {api}, live = {api,
// nvoi-{app}-{env}-db-main}, and the sweep deletes the DB StatefulSet
// on every deploy. Real-world break, real-world fixture.
//
// Fixture mirrors what the postgres provider actually emits via
// labels(req): managed-by + nvoi/database both set on the StatefulSet.
func TestListWorkloadNames_ExcludesDatabaseOwned(t *testing.T) {
	userDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "ns",
			Labels: map[string]string{
				utils.LabelAppManagedBy: utils.LabelManagedBy,
				utils.LabelAppName:      "api",
			},
		},
	}
	dbStatefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nvoi-myapp-prod-db-main", Namespace: "ns",
			Labels: map[string]string{
				utils.LabelAppManagedBy: utils.LabelManagedBy,
				utils.LabelAppName:      "nvoi-myapp-prod-db-main",
				utils.LabelNvoiDatabase: "main",
			},
		},
	}
	c := newTestClient(userDeployment, dbStatefulSet)

	names, err := c.ListWorkloadNames(context.Background(), "ns")
	if err != nil {
		t.Fatalf("ListWorkloadNames: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("len = %d (got %v), want 1 (only the user Deployment)", len(names), names)
	}
	if names[0] != "api" {
		t.Errorf("got %q, want api", names[0])
	}
}

// TestListWorkloadNames_DBOwnedDeploymentExcluded covers the
// Deployment branch (DB sidecars / branches deployed via Deployment
// rather than StatefulSet — already a possibility for branched DBs).
func TestListWorkloadNames_DBOwnedDeploymentExcluded(t *testing.T) {
	dbDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nvoi-myapp-prod-db-main-br-pr1", Namespace: "ns",
			Labels: map[string]string{
				utils.LabelAppManagedBy: utils.LabelManagedBy,
				utils.LabelNvoiDatabase: "main",
			},
		},
	}
	c := newTestClient(dbDeployment)

	names, err := c.ListWorkloadNames(context.Background(), "ns")
	if err != nil {
		t.Fatalf("ListWorkloadNames: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("DB-owned Deployment leaked through: %v", names)
	}
}

// TestListCronJobNames_ExcludesDatabaseOwned locks the equivalent
// guarantee for the cron orphan sweep — the production failure
// reproduced before the fix: deploy with backup: configured but no
// `crons:` block, the daily backup CronJob got deleted every reconcile
// because it matched NvoiSelector but wasn't in cfg.Crons.
//
// Fixture mirrors what BuildBackupCronJob emits: app.kubernetes.io/
// managed-by=nvoi + nvoi/database=main.
func TestListCronJobNames_ExcludesDatabaseOwned(t *testing.T) {
	userCron := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cleanup", Namespace: "ns",
			Labels: map[string]string{
				utils.LabelAppManagedBy: utils.LabelManagedBy,
				utils.LabelAppName:      "cleanup",
			},
		},
	}
	dbBackupCron := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nvoi-myapp-prod-db-main-backup", Namespace: "ns",
			Labels: map[string]string{
				utils.LabelAppManagedBy: utils.LabelManagedBy,
				utils.LabelAppName:      "nvoi-myapp-prod-db-main",
				utils.LabelNvoiDatabase: "main",
			},
		},
	}
	c := newTestClient(userCron, dbBackupCron)

	names, err := c.ListCronJobNames(context.Background(), "ns")
	if err != nil {
		t.Fatalf("ListCronJobNames: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("len = %d (got %v), want 1 (only the user cron)", len(names), names)
	}
	if names[0] != "cleanup" {
		t.Errorf("got %q, want cleanup", names[0])
	}
}
