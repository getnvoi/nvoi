package kube

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// LabelNode sets nvoi-role={role} on a Node. Idempotent — uses a JSON merge
// patch so re-runs are no-ops. Returns nil if the node doesn't exist yet
// (the labelling can race with k3s registering the node; the next deploy
// will catch up).
func (c *Client) LabelNode(ctx context.Context, nodeName, role string) error {
	patch := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{utils.LabelNvoiRole: role},
		},
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal label patch: %w", err)
	}
	_, err = c.cs.CoreV1().Nodes().Patch(ctx, nodeName, types.MergePatchType, data, metav1.PatchOptions{
		FieldManager: FieldManager,
	})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("label node %s: %w", nodeName, err)
	}
	return nil
}

// DrainAndRemoveNode drains workloads from a node and deletes it from the
// cluster. Self-healing: if drain fails on a NotReady node, force-removes it
// (workloads are already gone). Returns nil if the node doesn't exist.
//
// Drain logic: list pods bound to the node, evict each via the policy/v1
// Eviction API. Eviction respects PodDisruptionBudgets and triggers graceful
// shutdown — same behavior as `kubectl drain`.
func (c *Client) DrainAndRemoveNode(ctx context.Context, nodeName string) error {
	node, err := c.cs.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get node %s: %w", nodeName, err)
	}

	if drainErr := c.evictPods(ctx, nodeName); drainErr != nil {
		// If the node is NotReady (dead/unreachable), force-remove anyway.
		if !nodeReady(node) {
			return c.deleteNode(ctx, nodeName)
		}
		return fmt.Errorf("drain node %s: %w", nodeName, drainErr)
	}
	return c.deleteNode(ctx, nodeName)
}

// evictPods lists every pod scheduled on nodeName and evicts them via the
// policy/v1 Eviction API. DaemonSet pods are skipped (kubectl convention).
func (c *Client) evictPods(ctx context.Context, nodeName string) error {
	pods, err := c.cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return fmt.Errorf("list pods on %s: %w", nodeName, err)
	}
	for _, pod := range pods.Items {
		if isDaemonSetPod(&pod) {
			continue
		}
		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
		}
		if err := c.cs.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction); err != nil {
			if apierrors.IsNotFound(err) {
				continue // already gone
			}
			return fmt.Errorf("evict pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

func (c *Client) deleteNode(ctx context.Context, nodeName string) error {
	err := c.cs.CoreV1().Nodes().Delete(ctx, nodeName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete node %s: %w", nodeName, err)
	}
	return nil
}

func nodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}
