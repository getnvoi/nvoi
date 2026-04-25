package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// ownerLabels returns the standard label set ApplyOwned would stamp,
// useful for seeding fakes when testing SweepOwned in isolation.
func ownerLabels(owner string) map[string]string {
	return map[string]string{
		utils.LabelAppManagedBy: utils.LabelManagedBy,
		utils.LabelNvoiOwner:    owner,
	}
}

// TestSweepOwned_ScopedByOwner is the structural replacement for the
// old `LabelNvoiDatabase` exclusion in `ListCronJobNames`. The owner-
// scoped listing CANNOT see a different owner's resources, so a
// services-owner sweep can never touch a databases-owner CronJob even
// when desired is empty. This is the band-aid removed.
func TestSweepOwned_ScopedByOwner(t *testing.T) {
	userCron := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cleanup", Namespace: "ns",
			Labels: ownerLabels(utils.OwnerCrons),
		},
	}
	dbBackupCron := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nvoi-myapp-prod-db-main-backup", Namespace: "ns",
			Labels: ownerLabels(utils.OwnerDatabases),
		},
	}
	c := newTestClient(userCron, dbBackupCron)

	// Crons step sweeps with desired={cleanup} — the DB backup CronJob
	// MUST survive even though it isn't in desired, because its owner
	// label scopes it out of this sweep entirely.
	if err := c.SweepOwned(context.Background(), "ns", utils.OwnerCrons, KindCronJob, []string{"cleanup"}); err != nil {
		t.Fatalf("SweepOwned: %v", err)
	}

	if _, err := c.cs.BatchV1().CronJobs("ns").Get(context.Background(), "cleanup", metav1.GetOptions{}); err != nil {
		t.Errorf("desired user cron 'cleanup' was deleted: %v", err)
	}
	if _, err := c.cs.BatchV1().CronJobs("ns").Get(context.Background(), "nvoi-myapp-prod-db-main-backup", metav1.GetOptions{}); err != nil {
		t.Errorf("DB backup CronJob (owner=databases) eaten by services sweep — band-aid regression: %v", err)
	}
}

// TestSweepOwned_EmptyDesired_SweepsAll covers the migration-cleanup
// case (PurgeCaddy / PurgeTunnelAgents replacement): nil/empty desired
// means delete every resource for that owner+kind.
func TestSweepOwned_EmptyDesired_SweepsAll(t *testing.T) {
	a := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cloudflared", Namespace: "ns",
			Labels: ownerLabels(utils.OwnerTunnel),
		},
	}
	b := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ngrok", Namespace: "ns",
			Labels: ownerLabels(utils.OwnerTunnel),
		},
	}
	// Sibling owned by a different reconcile step — must not be touched.
	keep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "ns",
			Labels: ownerLabels(utils.OwnerServices),
		},
	}
	c := newTestClient(a, b, keep)

	if err := c.SweepOwned(context.Background(), "ns", utils.OwnerTunnel, KindDeployment, nil); err != nil {
		t.Fatalf("SweepOwned: %v", err)
	}

	for _, name := range []string{"cloudflared", "ngrok"} {
		if _, err := c.cs.AppsV1().Deployments("ns").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Errorf("tunnel/%s should be gone: %v", name, err)
		}
	}
	if _, err := c.cs.AppsV1().Deployments("ns").Get(context.Background(), "api", metav1.GetOptions{}); err != nil {
		t.Errorf("services/api was eaten by tunnel sweep — owner scoping broken: %v", err)
	}
}

// TestSweepOwned_KeepsDesired_DeletesOrphan locks the orphan-sweep
// shape itself — the only resource of the given owner+kind that
// matches a name in desired survives.
func TestSweepOwned_KeepsDesired_DeletesOrphan(t *testing.T) {
	keep := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "ns",
			Labels: ownerLabels(utils.OwnerServices),
		},
	}
	orphan := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "deprecated", Namespace: "ns",
			Labels: ownerLabels(utils.OwnerServices),
		},
	}
	c := newTestClient(keep, orphan)

	if err := c.SweepOwned(context.Background(), "ns", utils.OwnerServices, KindService, []string{"api"}); err != nil {
		t.Fatalf("SweepOwned: %v", err)
	}

	if _, err := c.cs.CoreV1().Services("ns").Get(context.Background(), "api", metav1.GetOptions{}); err != nil {
		t.Errorf("desired service deleted: %v", err)
	}
	if _, err := c.cs.CoreV1().Services("ns").Get(context.Background(), "deprecated", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("orphan service survived: %v", err)
	}
}

// TestSweepOwned_NoOwnerLabel_NotTouched covers the migration window:
// pre-fix clusters may have nvoi-managed resources without a
// `nvoi/owner` label. Those MUST NOT be silently deleted by sweeps —
// the operator's next deploy stamps owner via ApplyOwned and the
// sweep then converges normally.
func TestSweepOwned_NoOwnerLabel_NotTouched(t *testing.T) {
	legacy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "legacy", Namespace: "ns",
			Labels: map[string]string{
				utils.LabelAppManagedBy: utils.LabelManagedBy,
				// No utils.LabelNvoiOwner — pre-migration deploy.
			},
		},
	}
	c := newTestClient(legacy)

	// Sweep with empty desired — should NOT touch the legacy resource
	// because it doesn't carry the owner label.
	if err := c.SweepOwned(context.Background(), "ns", utils.OwnerServices, KindDeployment, nil); err != nil {
		t.Fatalf("SweepOwned: %v", err)
	}

	if _, err := c.cs.AppsV1().Deployments("ns").Get(context.Background(), "legacy", metav1.GetOptions{}); err != nil {
		t.Errorf("legacy resource without owner label was deleted — sweep is too aggressive: %v", err)
	}
}

// TestSweepOwned_AcrossKinds proves SweepOwned covers every kind a
// reconcile step might emit. Lock the supported kinds explicitly.
func TestSweepOwned_AcrossKinds(t *testing.T) {
	objs := []seededObject{
		{KindDeployment, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "ns", Labels: ownerLabels(utils.OwnerServices)}}},
		{KindStatefulSet, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns", Labels: ownerLabels(utils.OwnerServices)}}},
		{KindService, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns", Labels: ownerLabels(utils.OwnerServices)}}},
		{KindSecret, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: "ns", Labels: ownerLabels(utils.OwnerServices)}}},
		{KindConfigMap, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "ns", Labels: ownerLabels(utils.OwnerServices)}}},
		{KindCronJob, &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "cj", Namespace: "ns", Labels: ownerLabels(utils.OwnerServices)}}},
		{KindPVC, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns", Labels: ownerLabels(utils.OwnerServices)}}},
	}
	all := make([]runtime.Object, 0, len(objs))
	for _, o := range objs {
		all = append(all, o.obj.(runtime.Object))
	}
	c := newTestClient(all...)

	for _, o := range objs {
		if err := c.SweepOwned(context.Background(), "ns", utils.OwnerServices, o.kind, nil); err != nil {
			t.Fatalf("SweepOwned %s: %v", o.kind, err)
		}
	}

	// Spot-check: nothing of OwnerServices left.
	if list, _ := c.cs.AppsV1().Deployments("ns").List(context.Background(), metav1.ListOptions{LabelSelector: utils.LabelNvoiOwner + "=" + utils.OwnerServices}); len(list.Items) != 0 {
		t.Errorf("Deployments survived sweep: %d", len(list.Items))
	}
	if list, _ := c.cs.CoreV1().PersistentVolumeClaims("ns").List(context.Background(), metav1.ListOptions{LabelSelector: utils.LabelNvoiOwner + "=" + utils.OwnerServices}); len(list.Items) != 0 {
		t.Errorf("PVCs survived sweep: %d", len(list.Items))
	}
}

type seededObject struct {
	kind Kind
	obj  metav1.Object
}

// TestSweepOwned_RejectsUnknownKind locks the explicit-allowlist
// posture. Adding a new sweep target requires an explicit switch case.
func TestSweepOwned_RejectsUnknownKind(t *testing.T) {
	c := newTestClient()
	err := c.SweepOwned(context.Background(), "ns", utils.OwnerServices, Kind("Pod"), nil)
	if err == nil || !contains(err.Error(), "unsupported kind") {
		t.Errorf("expected unsupported-kind error, got: %v", err)
	}
}

// TestSweepOwned_EmptyOwner_Errors locks the API contract — silently
// running with owner="" would sweep across every owner, which is
// exactly the foot-gun owner-scoping is meant to prevent.
func TestSweepOwned_EmptyOwner_Errors(t *testing.T) {
	c := newTestClient()
	err := c.SweepOwned(context.Background(), "ns", "", KindDeployment, nil)
	if err == nil || !contains(err.Error(), "owner required") {
		t.Errorf("expected owner-required error, got: %v", err)
	}
}

// TestApplyOwned_StampsLabels_OnCreate proves ApplyOwned adds the
// managed-by + owner pair to a brand-new object before it lands in
// the cluster.
func TestApplyOwned_StampsLabels_OnCreate(t *testing.T) {
	c := newTestClient()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}}},
	}
	if err := c.ApplyOwned(context.Background(), "ns", utils.OwnerServices, dep); err != nil {
		t.Fatalf("ApplyOwned: %v", err)
	}

	got, err := c.cs.AppsV1().Deployments("ns").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Labels[utils.LabelAppManagedBy] != utils.LabelManagedBy {
		t.Errorf("missing managed-by label: %v", got.Labels)
	}
	if got.Labels[utils.LabelNvoiOwner] != utils.OwnerServices {
		t.Errorf("missing owner label: %v", got.Labels)
	}
}

// TestApplyOwned_StampsLabels_OnUpdate is the structural replacement
// for the EnsureSecret label-heal — re-applying an object with stale
// labels stamps the current owner+managed-by every time, no special
// "heal" code path needed.
func TestApplyOwned_StampsLabels_OnUpdate(t *testing.T) {
	stale := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "ns",
			Labels: map[string]string{"unrelated": "value"},
		},
		Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}}},
	}
	c := newTestClient(stale)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}}},
	}
	if err := c.ApplyOwned(context.Background(), "ns", utils.OwnerServices, dep); err != nil {
		t.Fatalf("ApplyOwned: %v", err)
	}

	got, err := c.cs.AppsV1().Deployments("ns").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Labels[utils.LabelAppManagedBy] != utils.LabelManagedBy {
		t.Errorf("managed-by not healed on Update: %v", got.Labels)
	}
	if got.Labels[utils.LabelNvoiOwner] != utils.OwnerServices {
		t.Errorf("owner not stamped on Update: %v", got.Labels)
	}
}

// TestApplyOwned_EmptyOwner_Errors mirrors SweepOwned's contract — no
// silent fallback to "no owner" because the owner label is the entire
// point of the discipline.
func TestApplyOwned_EmptyOwner_Errors(t *testing.T) {
	c := newTestClient()
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"}}
	err := c.ApplyOwned(context.Background(), "ns", "", dep)
	if err == nil || !contains(err.Error(), "owner required") {
		t.Errorf("expected owner-required error, got: %v", err)
	}
}
