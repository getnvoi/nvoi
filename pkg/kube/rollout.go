package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// ProgressEmitter receives status updates during rollout polling.
// app.Output satisfies this — defined here so kube/ doesn't import app/.
type ProgressEmitter interface {
	Progress(msg string)
}

// rolloutPollInterval is the interval between readiness polls.
var rolloutPollInterval = 3 * time.Second

// rolloutTimeout is the maximum time to wait for all pods to become ready.
var rolloutTimeout = 5 * time.Minute

// stabilityDelay is the pause between "all ready" and the verification poll.
var stabilityDelay = 4 * time.Second

// SetTestTiming overrides poll interval and stability delay for tests.
func SetTestTiming(poll, stability time.Duration) {
	rolloutPollInterval = poll
	stabilityDelay = stability
}

// hardFailReason returns a non-empty reason when a container state is
// genuinely unrecoverable and further polling is pointless. Covers the
// exhaustive set: image can't be fetched, config is malformed, scheduler
// can't place the pod, container keeps being OOM-killed. Every other
// state (CrashLoopBackOff, plain Error exit, probe failing, etc.) is
// treated as transient — the outer rolloutTimeout is the only bailout.
func hardFailReason(pod *corev1.Pod) string {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == "Unschedulable" {
			return fmt.Sprintf("pod %s unschedulable — %s", pod.Name, cond.Message)
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull":
				return fmt.Sprintf("%s — %s", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			case "CreateContainerConfigError":
				return fmt.Sprintf("%s — %s", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			}
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
			return "OOMKilled — container ran out of memory"
		}
	}
	return ""
}

// WaitRollout polls pods by label until all are Ready. Philosophy matches
// kubectl rollout status: wait patiently until the rollout converges OR a
// genuinely unrecoverable state is observed.
//
// Unrecoverable (bail immediately): Unschedulable, ImagePullBackOff family,
// CreateContainerConfigError, OOMKilled. Nothing kubelet does will ever
// make these work without the operator intervening.
//
// Everything else — CrashLoopBackOff with any restart count, plain Error
// exits, probe failing, Scheduling, ContainerCreating — is transient. Keep
// polling; kubelet + the controller will converge or the outer
// rolloutTimeout will bail us out with diagnostics.
func (c *Client) WaitRollout(ctx context.Context, ns, name, kind string, hasHealthCheck bool, emitter ProgressEmitter) error {
	selector := PodSelector(name)
	lastStatus := ""

	// Track the initial restart count for each pod so we can detect crashes
	// that happen after the pod briefly reaches Ready (verifyStability).
	initialRestarts := map[string]int{}

	err := utils.Poll(ctx, rolloutPollInterval, rolloutTimeout, func() (bool, error) {
		pods, err := c.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return false, nil // transient — retry
		}
		if len(pods.Items) == 0 {
			return false, nil
		}

		for i := range pods.Items {
			pod := &pods.Items[i]
			if _, tracked := initialRestarts[pod.Name]; !tracked {
				total := 0
				for _, cs := range pod.Status.ContainerStatuses {
					total += int(cs.RestartCount)
				}
				initialRestarts[pod.Name] = total
			}
		}

		ready := 0
		total := len(pods.Items)
		var states []string
		var probeFailPod string

		for i := range pods.Items {
			pod := &pods.Items[i]

			// Unrecoverable state → bail. No retries help.
			if reason := hardFailReason(pod); reason != "" {
				logs := c.RecentLogs(ctx, ns, name, kind, 30)
				if logs != "" {
					return false, fmt.Errorf("%s: %s\nlogs:\n%s", name, reason, indent(logs, "  "))
				}
				return false, fmt.Errorf("%s: %s", name, reason)
			}

			if pod.Status.Phase == corev1.PodSucceeded {
				ready++
				continue
			}

			// Observe state for progress reporting; don't fail on transient.
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Ready {
					continue
				}
				if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
					if cs.State.Waiting.Reason == "CrashLoopBackOff" && cs.RestartCount > 0 {
						states = append(states, fmt.Sprintf("CrashLoopBackOff (restarts: %d)", cs.RestartCount))
					} else {
						states = append(states, cs.State.Waiting.Reason)
					}
				}
				if cs.State.Terminated != nil {
					reason := cs.State.Terminated.Reason
					if reason == "" {
						reason = "Error"
					}
					states = append(states, fmt.Sprintf("%s exit=%d", reason, cs.State.Terminated.ExitCode))
				}
				if cs.State.Running != nil {
					if probeFailPod == "" {
						probeFailPod = pod.Name
					}
					states = append(states, "probe failing")
				}
			}

			if pod.Status.Phase == corev1.PodRunning {
				allReady := true
				for _, cs := range pod.Status.ContainerStatuses {
					if !cs.Ready {
						allReady = false
					}
				}
				if allReady {
					ready++
				}
			} else if pod.Status.Phase == corev1.PodPending && len(pod.Status.ContainerStatuses) == 0 {
				states = append(states, "Scheduling")
			}
		}

		if probeFailPod != "" {
			if detail := c.probeFailureDetail(ctx, ns, probeFailPod); detail != "" {
				for i, s := range states {
					if s == "probe failing" {
						states[i] = "probe failing: " + detail
						break
					}
				}
			}
		}

		status := fmt.Sprintf("%d/%d ready", ready, total)
		if len(states) > 0 {
			status += " (" + strings.Join(dedup(states), ", ") + ")"
		}
		if status != lastStatus {
			emitter.Progress(name + ": " + status)
			lastStatus = status
		}

		return ready == total, nil
	})
	if errors.Is(err, utils.ErrTimeout) {
		return c.timeoutDiagnostics(ctx, ns, name, kind, selector, lastStatus)
	}
	if err != nil {
		return err
	}

	if hasHealthCheck {
		return nil
	}

	emitter.Progress(name + ": verifying stability")

	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-time.After(stabilityDelay):
	}

	return c.verifyStability(ctx, ns, name, kind, selector, initialRestarts, emitter)
}

// verifyStability re-polls pods after the stability delay and fails if any
// pod's restart count increased since tracking began.
func (c *Client) verifyStability(ctx context.Context, ns, name, kind, selector string, initialRestarts map[string]int, emitter ProgressEmitter) error {
	pods, err := c.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("%s: stability check failed: %w", name, err)
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		currentTotal := 0
		for _, cs := range pod.Status.ContainerStatuses {
			currentTotal += int(cs.RestartCount)
		}
		initial, tracked := initialRestarts[pod.Name]
		if tracked && currentTotal > initial {
			logs := c.RecentLogs(ctx, ns, name, kind, 20)
			return fmt.Errorf("%s: pod crashed after becoming ready (restarts: %d)\nlogs:\n%s", name, currentTotal, indent(logs, "  "))
		}

		if reason := hardFailReason(pod); reason != "" {
			logs := c.RecentLogs(ctx, ns, name, kind, 20)
			if logs != "" {
				return fmt.Errorf("%s: %s\nlogs:\n%s", name, reason, indent(logs, "  "))
			}
			return fmt.Errorf("%s: %s", name, reason)
		}
	}

	return nil
}

// RecentLogs fetches the last lines from a pod or workload via the streaming
// log endpoint. Tries --previous first — on CrashLoopBackOff the current
// container just started and has no logs yet; the useful logs (the actual
// crash) live in the previous container instance.
func (c *Client) RecentLogs(ctx context.Context, ns, name, kind string, tail int) string {
	if tail == 0 {
		tail = 20
	}

	// For workload kinds, resolve to the first matching pod by label.
	podName := name
	if kind != "" {
		got, err := c.FirstPod(ctx, ns, name)
		if err != nil {
			return ""
		}
		podName = got
	}

	// Previous-container logs first — on a crash loop these are what the
	// operator needs. Fall through to current if previous is empty (no
	// prior restart yet, or kubelet hasn't persisted them).
	if prev := c.podLogs(ctx, ns, podName, &corev1.PodLogOptions{
		Previous:  true,
		TailLines: int64Ptr(int64(tail)),
	}); prev != "" {
		return prev
	}

	return c.podLogs(ctx, ns, podName, &corev1.PodLogOptions{
		TailLines: int64Ptr(int64(tail)),
	})
}

func (c *Client) podLogs(ctx context.Context, ns, podName string, opts *corev1.PodLogOptions) string {
	req := c.cs.CoreV1().Pods(ns).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func int64Ptr(v int64) *int64 { return &v }

// probeFailureDetail fetches the latest Warning event for a pod and returns
// its message, truncated for single-line status display.
func (c *Client) probeFailureDetail(ctx context.Context, ns, podName string) string {
	events := c.recentEvents(ctx, ns, podName)
	for _, ev := range events {
		if ev.Message != "" {
			return truncate(ev.Message, 80)
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func dedup(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
