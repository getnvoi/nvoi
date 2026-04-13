package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// podStatus and podList are aliases to the shared types in types.go.
type podStatus = PodItem
type podList = PodList

// WaitRollout polls pods by label until all are Ready, printing state changes.
// Terminal failures (bad image, config error, crash loop) exit immediately.
// Transient states (scheduling, pulling, creating) keep polling with feedback.
// ProgressEmitter receives status updates during rollout polling.
// Defined here so kube/ doesn't import app/. app.Output satisfies this.
type ProgressEmitter interface {
	Progress(msg string)
}

// rolloutPollInterval is the interval between readiness polls.
var rolloutPollInterval = 3 * time.Second

// stabilityDelay is the pause between "all ready" and the verification poll.
var stabilityDelay = 4 * time.Second

// SetTestTiming overrides poll interval and stability delay for tests.
func SetTestTiming(poll, stability time.Duration) {
	rolloutPollInterval = poll
	stabilityDelay = stability
}

func WaitRollout(ctx context.Context, ssh utils.SSHClient, ns, name, kind string, hasHealthCheck bool, emitter ProgressEmitter) error {
	selector := fmt.Sprintf("%s=%s", utils.LabelAppName, name)
	lastStatus := ""

	// Track the initial restart count for each pod so we can detect crashes
	// that happen after the pod briefly reaches Ready.
	initialRestarts := map[string]int{}

	err := utils.Poll(ctx, rolloutPollInterval, 5*time.Minute, func() (bool, error) {
		cmd := kctl(ns, fmt.Sprintf("get pods -l %s -o json", selector))
		out, err := ssh.Run(ctx, cmd)
		if err != nil {
			return false, nil // transient — retry
		}

		var pods podList
		if err := json.Unmarshal(out, &pods); err != nil {
			return false, nil
		}

		if len(pods.Items) == 0 {
			return false, nil
		}

		// Record initial restart counts the first time we see each pod.
		for _, pod := range pods.Items {
			if _, tracked := initialRestarts[pod.Metadata.Name]; !tracked {
				total := 0
				for _, cs := range pod.Status.ContainerStatuses {
					total += cs.RestartCount
				}
				initialRestarts[pod.Metadata.Name] = total
			}
		}

		ready := 0
		total := len(pods.Items)
		var states []string

		for _, pod := range pods.Items {
			// Check for unschedulable — terminal
			for _, cond := range pod.Status.Conditions {
				if cond.Type == "PodScheduled" && cond.Status == "False" && cond.Reason == "Unschedulable" {
					return false, fmt.Errorf("%s: pod %s unschedulable — %s", name, pod.Metadata.Name, cond.Message)
				}
			}

			if pod.Status.Phase == "Succeeded" {
				ready++
				continue
			}

			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Ready {
					continue
				}

				// Terminal — exit immediately
				if cs.State.Waiting != nil {
					reason := cs.State.Waiting.Reason
					switch reason {
					case "ImagePullBackOff", "ErrImagePull", "InvalidImageName":
						return false, fmt.Errorf("%s: %s — %s", name, reason, cs.State.Waiting.Message)
					case "CreateContainerConfigError":
						return false, fmt.Errorf("%s: %s — %s", name, reason, cs.State.Waiting.Message)
					case "CrashLoopBackOff":
						logs := RecentLogs(ctx, ssh, ns, name, kind, 20)
						return false, fmt.Errorf("%s: CrashLoopBackOff (restarts: %d)\nlogs:\n%s", name, cs.RestartCount, indent(logs, "  "))
					}
					// Transient — keep polling
					if reason != "" {
						states = append(states, reason)
					}
				}
				if cs.State.Terminated != nil {
					reason := cs.State.Terminated.Reason
					if reason == "OOMKilled" {
						return false, fmt.Errorf("%s: OOMKilled — container ran out of memory", name)
					}
					if reason != "" {
						states = append(states, reason)
					}
				}
			}

			if pod.Status.Phase == "Running" {
				allReady := true
				for _, cs := range pod.Status.ContainerStatuses {
					if !cs.Ready {
						allReady = false
					}
				}
				if allReady {
					ready++
				}
			} else if pod.Status.Phase == "Pending" && len(pod.Status.ContainerStatuses) == 0 {
				states = append(states, "Scheduling")
			}
		}

		// Build status line, print only on change
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
	if err != nil {
		return err
	}

	// Services with a health check (readiness probe) don't need the extra
	// stability check — k8s won't mark the pod Ready until the probe passes,
	// so CrashLoopBackOff is detected naturally during polling above.
	if hasHealthCheck {
		return nil
	}

	// No health check: wait briefly and re-check for post-startup crashes.
	// Apps without a readiness probe can briefly reach Ready then crash.
	emitter.Progress(name + ": verifying stability")

	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-time.After(stabilityDelay):
	}

	return verifyStability(ctx, ssh, ns, name, kind, selector, initialRestarts, emitter)
}

// verifyStability re-polls pods after the stability delay and fails if any
// pod's restart count increased since tracking began — indicating a post-startup crash.
func verifyStability(ctx context.Context, ssh utils.SSHClient, ns, name, kind, selector string, initialRestarts map[string]int, emitter ProgressEmitter) error {
	cmd := kctl(ns, fmt.Sprintf("get pods -l %s -o json", selector))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("%s: stability check failed: %w", name, err)
	}

	var pods podList
	if err := json.Unmarshal(out, &pods); err != nil {
		return fmt.Errorf("%s: stability check failed: %w", name, err)
	}

	for _, pod := range pods.Items {
		// Detect crash-after-ready: total restart count increased for this pod.
		currentTotal := 0
		for _, cs := range pod.Status.ContainerStatuses {
			currentTotal += cs.RestartCount
		}
		initial, tracked := initialRestarts[pod.Metadata.Name]
		if tracked && currentTotal > initial {
			logs := RecentLogs(ctx, ssh, ns, name, kind, 20)
			return fmt.Errorf("%s: pod crashed after becoming ready (restarts: %d)\nlogs:\n%s", name, currentTotal, indent(logs, "  "))
		}

		// Also check for terminal states that appeared after the ready window.
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				continue
			}
			if cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				switch reason {
				case "CrashLoopBackOff":
					logs := RecentLogs(ctx, ssh, ns, name, kind, 20)
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

// RecentLogs fetches the last lines from a pod or workload.
// For pods: kind="" and name is the pod name. Tries --previous first (crashed container).
// For workloads: kind="deployment" or "statefulset", name is the workload name.
func RecentLogs(ctx context.Context, ssh utils.SSHClient, ns, name, kind string, tail int) string {
	if tail == 0 {
		tail = 20
	}
	target := name
	if kind != "" {
		target = kind + "/" + name
	}

	// For bare pods (no kind), try --previous first to get crashed container logs
	if kind == "" {
		out, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("logs %s --previous --tail=%d", target, tail)))
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			return strings.TrimSpace(string(out))
		}
	}

	out, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("logs %s --tail=%d", target, tail)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
