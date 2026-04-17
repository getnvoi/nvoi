package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestLabelNode_AppliesLabel(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "master"}}
	c := newTestClient(node)

	if err := c.LabelNode(context.Background(), "master", "master"); err != nil {
		t.Fatalf("label: %v", err)
	}
	got, err := c.cs.CoreV1().Nodes().Get(context.Background(), "master", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Labels[utils.LabelNvoiRole] != "master" {
		t.Errorf("labels = %v, want nvoi-role=master", got.Labels)
	}
}

func TestLabelNode_NodeMissing_NoError(t *testing.T) {
	c := newTestClient()
	// Node not yet registered with k3s — must not block deploy.
	if err := c.LabelNode(context.Background(), "worker-1", "worker"); err != nil {
		t.Errorf("missing node must not error: %v", err)
	}
}

func TestDrainAndRemoveNode_Idempotent_Missing(t *testing.T) {
	c := newTestClient()
	if err := c.DrainAndRemoveNode(context.Background(), "gone"); err != nil {
		t.Errorf("absent node must not error: %v", err)
	}
}

func TestDrainAndRemoveNode_ReadyNode_Deletes(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
	c := newTestClient(node)

	if err := c.DrainAndRemoveNode(context.Background(), "worker-1"); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if _, err := c.cs.CoreV1().Nodes().Get(context.Background(), "worker-1", metav1.GetOptions{}); err == nil {
		t.Error("node should be gone")
	}
}

func TestDrainAndRemoveNode_NotReady_ForceRemoves(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
		},
	}
	c := newTestClient(node)

	if err := c.DrainAndRemoveNode(context.Background(), "worker-1"); err != nil {
		t.Fatalf("force-remove dead node: %v", err)
	}
	if _, err := c.cs.CoreV1().Nodes().Get(context.Background(), "worker-1", metav1.GetOptions{}); err == nil {
		t.Error("dead node should still get removed")
	}
}

func TestNodeReady(t *testing.T) {
	ready := &corev1.Node{
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	if !nodeReady(ready) {
		t.Error("Ready=True must be ready")
	}
	notReady := &corev1.Node{
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}},
		},
	}
	if nodeReady(notReady) {
		t.Error("Ready=False must not be ready")
	}
	noStatus := &corev1.Node{}
	if nodeReady(noStatus) {
		t.Error("no condition must not be ready")
	}
}

func TestIsDaemonSetPod(t *testing.T) {
	ds := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet"}},
		},
	}
	if !isDaemonSetPod(ds) {
		t.Error("DaemonSet owner must be detected")
	}
	plain := &corev1.Pod{}
	if isDaemonSetPod(plain) {
		t.Error("no owner must not be detected as DS")
	}
}
