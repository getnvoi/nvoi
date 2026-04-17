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

// WaitRollout polls pods by label until all are Ready, printing state changes.
// Terminal failures (bad image, config error, crash loop) exit immediately.
// Transient states (scheduling, pulling, creating) keep polling with feedback.
func (c *Client) WaitRollout(ctx context.Context, ns, name, kind string, hasHealthCheck bool, emitter ProgressEmitter) error {
	selector := PodSelector(name)
	lastStatus := ""

	// Track the initial restart count for each pod so we can detect crashes
	// that happen after the pod briefly reaches Ready.
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
			// Check for unschedulable — terminal
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == "Unschedulable" {
					return false, fmt.Errorf("%s: pod %s unschedulable — %s", name, pod.Name, cond.Message)
				}
			}

			if pod.Status.Phase == corev1.PodSucceeded {
				ready++
				continue
			}

			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Ready {
					continue
				}

				if cs.State.Waiting != nil {
					reason := cs.State.Waiting.Reason
					switch reason {
					case "ImagePullBackOff", "ErrImagePull", "InvalidImageName":
						return false, fmt.Errorf("%s: %s — %s", name, reason, cs.State.Waiting.Message)
					case "CreateContainerConfigError":
						return false, fmt.Errorf("%s: %s — %s", name, reason, cs.State.Waiting.Message)
					case "CrashLoopBackOff":
						logs := c.RecentLogs(ctx, ns, name, kind, 20)
						return false, fmt.Errorf("%s: CrashLoopBackOff (restarts: %d)\nlogs:\n%s", name, cs.RestartCount, indent(logs, "  "))
					}
					if reason != "" {
						states = append(states, reason)
					}
				}
				if cs.State.Terminated != nil {
					reason := cs.State.Terminated.Reason
					exitCode := cs.State.Terminated.ExitCode
					switch reason {
					case "OOMKilled":
						return false, fmt.Errorf("%s: OOMKilled — container ran out of memory", name)
					case "Error", "":
						if cs.RestartCount > 0 {
							logs := c.RecentLogs(ctx, ns, name, kind, 30)
							return false, fmt.Errorf("%s: container exited with code %d (restarts: %d)\nlogs:\n%s",
								name, exitCode, cs.RestartCount, indent(logs, "  "))
						}
						states = append(states, fmt.Sprintf("Error (exit %d)", exitCode))
					default:
						if reason != "" {
							states = append(states, reason)
						}
					}
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

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				continue
			}
			if cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				switch reason {
				case "CrashLoopBackOff":
					logs := c.RecentLogs(ctx, ns, name, kind, 20)
					return fmt.Errorf("%s: CrashLoopBackOff (restarts: %d)\nlogs:\n%s", name, cs.RestartCount, indent(logs, "  "))
				case "ImagePullBackOff", "ErrImagePull", "InvalidImageName",
					"CreateContainerConfigError":
					return fmt.Errorf("%s: %s — %s", name, reason, cs.State.Waiting.Message)
				}
			}
			if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
				return fmt.Errorf("%s: OOMKilled — container ran out of memory", name)
			}
		}
	}

	return nil
}

// RecentLogs fetches the last lines from a pod or workload via the streaming
// log endpoint. For bare pods (kind=="") tries --previous first to capture
// crashed-container logs.
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

	// For bare pods, try previous-container logs first (catches crashes).
	if kind == "" {
		if prev := c.podLogs(ctx, ns, podName, &corev1.PodLogOptions{
			Previous:  true,
			TailLines: int64Ptr(int64(tail)),
		}); prev != "" {
			return prev
		}
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
