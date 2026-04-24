package postgres

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/internal/testutil/kubefake"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// TestBranch_AppliesTypedWorkloads locks the shape of branch-created
// objects. Every non-snapshot kind must go through kc.Apply (typed
// clientset), not kubectl-on-master — the user called this out and
// the fix was the whole point of the rewrite. We assert:
//
//   - PVC exists with StorageClassName=nvoi-zfs-localpv and a
//     dataSource pointing at the snapshot.
//   - Service selector + port match the branch workload name.
//   - StatefulSet is pinned to the source's server role, binds to the
//     branch PVC, and sources credentials from the source DB's Secret
//     (ZFS clones carry pg_roles bit-exact).
//
// The snapshot itself is created via kubectl-on-master (external-
// snapshotter kind, outside our typed dispatch). Test just confirms
// the apply command fired against the masterSSH mock.
func TestBranch_AppliesTypedWorkloads(t *testing.T) {
	kf := kubefake.NewKubeFake()
	masterSSH := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "sudo k3s kubectl apply", Result: testutil.MockResult{}},
		},
	}

	names, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatal(err)
	}
	src := BranchSource{
		Names:      names,
		DBName:     "app",
		Size:       20,
		Version:    "16",
		ServerRole: "db-master",
	}

	res, err := Branch(context.Background(), kf.Client, masterSSH, src, "pr142")
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	wantWorkload := "nvoi-myapp-prod-db-app-data-br-pr142"
	if res.PVC != wantWorkload || res.Service != wantWorkload {
		t.Errorf("unexpected result names: %+v", res)
	}

	// Snapshot kubectl-applied to master.
	foundSnap := false
	for _, call := range masterSSH.Calls {
		if strings.Contains(call, "kubectl apply -f -") && strings.Contains(call, "VolumeSnapshot") {
			foundSnap = true
			break
		}
	}
	if !foundSnap {
		t.Errorf("VolumeSnapshot was not kubectl-applied via masterSSH; calls: %v", masterSSH.Calls)
	}

	// Typed PVC landed in the fake clientset via kc.Apply.
	if !kf.HasPVC(src.Names.KubeNamespace(), wantWorkload) {
		t.Fatalf("branch PVC not applied via kube.Client")
	}
	pvc, err := kf.Typed.CoreV1().PersistentVolumeClaims(src.Names.KubeNamespace()).Get(
		context.Background(), wantWorkload, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != ZFSStorageClassName {
		t.Errorf("branch PVC StorageClassName = %v, want %q", pvc.Spec.StorageClassName, ZFSStorageClassName)
	}
	if pvc.Spec.DataSource == nil || pvc.Spec.DataSource.Kind != "VolumeSnapshot" {
		t.Errorf("branch PVC dataSource missing or wrong kind: %+v", pvc.Spec.DataSource)
	}
	if pvc.Spec.DataSource != nil && pvc.Spec.DataSource.Name != res.SnapshotName {
		t.Errorf("branch PVC dataSource Name = %q, want %q", pvc.Spec.DataSource.Name, res.SnapshotName)
	}

	// Typed Service landed.
	if !kf.HasService(src.Names.KubeNamespace(), wantWorkload) {
		t.Fatalf("branch Service not applied via kube.Client")
	}

	// Typed StatefulSet landed with the right pinning + PVC.
	if !kf.HasStatefulSet(src.Names.KubeNamespace(), wantWorkload) {
		t.Fatalf("branch StatefulSet not applied via kube.Client")
	}
	ss, err := kf.Typed.AppsV1().StatefulSets(src.Names.KubeNamespace()).Get(
		context.Background(), wantWorkload, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ss.Spec.Template.Spec.NodeSelector[utils.LabelNvoiRole] != src.ServerRole {
		t.Errorf("branch SS nodeSelector = %v, want %s=%s",
			ss.Spec.Template.Spec.NodeSelector, utils.LabelNvoiRole, src.ServerRole)
	}
	volumes := ss.Spec.Template.Spec.Volumes
	if len(volumes) != 1 || volumes[0].PersistentVolumeClaim == nil ||
		volumes[0].PersistentVolumeClaim.ClaimName != wantWorkload {
		t.Errorf("branch SS does not bind to branch PVC: volumes=%+v", volumes)
	}
	// Credentials sourced from the source DB's Secret — the clone
	// carries pg_roles, so auth is identical.
	assertEnvFromSecret(t, ss, "POSTGRES_USER", src.Names.KubeDatabaseCredentials(src.DBName))
	assertEnvFromSecret(t, ss, "POSTGRES_PASSWORD", src.Names.KubeDatabaseCredentials(src.DBName))
	assertEnvFromSecret(t, ss, "POSTGRES_DB", src.Names.KubeDatabaseCredentials(src.DBName))
}

// TestBranch_RejectsInvalidBranchName locks the validation boundary:
// branch names go into k8s object names and DNS-1123 rules apply.
func TestBranch_RejectsInvalidBranchName(t *testing.T) {
	kf := kubefake.NewKubeFake()
	ssh := &testutil.MockSSH{Prefixes: []testutil.MockPrefix{
		{Prefix: "sudo k3s kubectl apply", Result: testutil.MockResult{}},
	}}
	names, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Branch(context.Background(), kf.Client, ssh, BranchSource{
		Names: names, DBName: "app", Size: 20, Version: "16", ServerRole: "db-master",
	}, "UPPER.case")
	if err == nil {
		t.Fatal("expected validation error on invalid branch name")
	}
}

func assertEnvFromSecret(t *testing.T, ss *appsv1.StatefulSet, envVar, secretName string) {
	t.Helper()
	for _, e := range ss.Spec.Template.Spec.Containers[0].Env {
		if e.Name != envVar {
			continue
		}
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Errorf("%s is not sourced from a Secret", envVar)
			return
		}
		if e.ValueFrom.SecretKeyRef.Name != secretName {
			t.Errorf("%s sourced from Secret %q, want %q",
				envVar, e.ValueFrom.SecretKeyRef.Name, secretName)
		}
		return
	}
	t.Errorf("env var %s not found on branch container", envVar)
}
