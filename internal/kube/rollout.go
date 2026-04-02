package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/internal/core"
)

// podStatus is the subset of pod JSON we parse.
type podStatus struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Phase      string `json:"phase"`
		Conditions []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
		ContainerStatuses []struct {
			Ready        bool `json:"ready"`
			RestartCount int  `json:"restartCount"`
			State        struct {
				Waiting *struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"waiting"`
				Running    *struct{} `json:"running"`
				Terminated *struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"terminated"`
			} `json:"state"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type podList struct {
	Items []podStatus `json:"items"`
}

// WaitRollout polls pods by label until all are Ready, printing state changes.
// Terminal failures (bad image, config error, crash loop) exit immediately.
// Transient states (scheduling, pulling, creating) keep polling with feedback.
// ProgressEmitter receives status updates during rollout polling.
// Defined here so kube/ doesn't import app/. app.Output satisfies this.
type ProgressEmitter interface {
	Progress(msg string)
}

func WaitRollout(ctx context.Context, ssh core.SSHClient, ns, name, kind string, emitter ProgressEmitter) error {
	selector := fmt.Sprintf("%s=%s", core.LabelAppName, name)
	lastStatus := ""

	return core.Poll(ctx, 3*time.Second, 5*time.Minute, func() (bool, error) {
		cmd := kubectl(ns, fmt.Sprintf("get pods -l %s -o json", selector))
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
						logs := recentLogs(ctx, ssh, ns, name, kind)
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
}

// WaitPods polls until all pods in the namespace are Running or Succeeded.
func WaitPods(ctx context.Context, ssh core.SSHClient, ns string) error {
	return core.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		out, err := ssh.Run(ctx, kubectl(ns, "get pods -o json"))
		if err != nil {
			return false, nil
		}
		var pods podList
		if err := json.Unmarshal(out, &pods); err != nil {
			return false, nil
		}
		for _, pod := range pods.Items {
			phase := pod.Status.Phase
			if phase != "Running" && phase != "Succeeded" {
				return false, nil
			}
		}
		return true, nil
	})
}

func recentLogs(ctx context.Context, ssh core.SSHClient, ns, name, kind string) string {
	out, err := ssh.Run(ctx, kubectl(ns, fmt.Sprintf("logs %s/%s --tail=20", kind, name)))
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
