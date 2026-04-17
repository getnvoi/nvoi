package reconcile

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/getnvoi/nvoi/internal/testutil/kubefake"
)

// seedReadyNode inserts a Node marked Ready=True into the kube fake.
func seedReadyNode(t *testing.T, kf *kubefake.KubeFake, name string) {
	t.Helper()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
	if _, err := kf.Typed.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed ready node %q: %v", name, err)
	}
}

// seedNotReadyNode inserts a Node marked Ready=False into the kube fake.
func seedNotReadyNode(t *testing.T, kf *kubefake.KubeFake, name string) {
	t.Helper()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
		},
	}
	if _, err := kf.Typed.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed not-ready node %q: %v", name, err)
	}
}

// seedPodOnNode creates a Pod pinned to nodeName. Used to give
// DrainAndRemoveNode something to evict.
func seedPodOnNode(t *testing.T, kf *kubefake.KubeFake, ns, name, nodeName string) {
	t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{NodeName: nodeName},
	}
	if _, err := kf.Typed.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed pod %q on %q: %v", name, nodeName, err)
	}
}

// failEvictions installs a reactor that makes every eviction attempt fail
// with the given error. This simulates PodDisruptionBudget blocks or an
// unreachable kubelet.
func failEvictions(kf *kubefake.KubeFake, err error) {
	kf.Typed.PrependReactor("create", "pods/eviction", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
}
