package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// timeoutDiagnostics gathers pod state, events, and logs when rollout times out.
// Returns a rich error with actionable information instead of the bare
// "poll: timeout exceeded".
func (c *Client) timeoutDiagnostics(ctx context.Context, ns, name, kind, selector, lastStatus string) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s: timed out (%s)", name, lastStatus))

	pods, err := c.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return errors.New(b.String())
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if podReady(pod) {
			continue
		}

		b.WriteString(fmt.Sprintf("\n\npod %s (phase: %s):", pod.Name, pod.Status.Phase))

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				continue
			}
			if cs.State.Running != nil {
				b.WriteString("\n  running but not ready — readiness probe failing")
			} else if cs.State.Waiting != nil {
				b.WriteString(fmt.Sprintf("\n  waiting: %s", cs.State.Waiting.Reason))
				if cs.State.Waiting.Message != "" {
					b.WriteString(fmt.Sprintf(" — %s", cs.State.Waiting.Message))
				}
			} else if cs.State.Terminated != nil {
				b.WriteString(fmt.Sprintf("\n  terminated: exit %d", cs.State.Terminated.ExitCode))
				if cs.State.Terminated.Reason != "" {
					b.WriteString(fmt.Sprintf(" (%s)", cs.State.Terminated.Reason))
				}
			}
			if cs.RestartCount > 0 {
				b.WriteString(fmt.Sprintf("\n  restarts: %d", cs.RestartCount))
			}
		}

		for _, ev := range c.recentEvents(ctx, ns, pod.Name) {
			msg := ev.Message
			if ev.Count > 1 {
				msg += fmt.Sprintf(" (x%d)", ev.Count)
			}
			b.WriteString(fmt.Sprintf("\n  event: %s", msg))
		}
	}

	logs := c.RecentLogs(ctx, ns, name, kind, 30)
	if logs != "" {
		b.WriteString("\n\nlogs:\n")
		b.WriteString(indent(logs, "  "))
	}

	return errors.New(b.String())
}

// podReady returns true if all containers in the pod are Ready.
func podReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodSucceeded {
		return false
	}
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}

// recentEvents fetches Warning events for a specific pod via the typed
// Events API.
func (c *Client) recentEvents(ctx context.Context, ns, podName string) []corev1.Event {
	list, err := c.cs.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,type=Warning", podName),
	})
	if err != nil {
		return nil
	}
	return list.Items
}
