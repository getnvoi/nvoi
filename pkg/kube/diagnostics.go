package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// timeoutDiagnostics gathers pod state, events, and logs when rollout times out.
// Returns a rich error with actionable information instead of the bare "poll: timeout exceeded".
func timeoutDiagnostics(ctx context.Context, ssh utils.SSHClient, ns, name, kind, selector, lastStatus string) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s: timed out (%s)", name, lastStatus))

	// Re-query pods one final time for current state
	cmd := kctl(ns, fmt.Sprintf("get pods -l %s -o json", selector))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return errors.New(b.String())
	}
	var pods podList
	if err := json.Unmarshal(out, &pods); err != nil {
		return errors.New(b.String())
	}

	for _, pod := range pods.Items {
		if podReady(pod) {
			continue
		}

		b.WriteString(fmt.Sprintf("\n\npod %s (phase: %s):", pod.Metadata.Name, pod.Status.Phase))

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

		// Fetch Warning events — shows probe failure details, scheduling issues, etc.
		events := recentEvents(ctx, ssh, ns, pod.Metadata.Name)
		for _, ev := range events {
			msg := ev.Message
			if ev.Count > 1 {
				msg += fmt.Sprintf(" (x%d)", ev.Count)
			}
			b.WriteString(fmt.Sprintf("\n  event: %s", msg))
		}
	}

	// Fetch workload logs
	logs := RecentLogs(ctx, ssh, ns, name, kind, 30)
	if logs != "" {
		b.WriteString("\n\nlogs:\n")
		b.WriteString(indent(logs, "  "))
	}

	return errors.New(b.String())
}

// podReady returns true if all containers in the pod are Ready.
func podReady(pod PodItem) bool {
	if pod.Status.Phase != "Running" && pod.Status.Phase != "Succeeded" {
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

// recentEvents fetches Warning events for a specific pod.
func recentEvents(ctx context.Context, ssh utils.SSHClient, ns, podName string) []EventItem {
	cmd := kctl(ns, fmt.Sprintf("get events --field-selector involvedObject.name=%s,type=Warning -o json", podName))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return nil
	}
	var events EventList
	if err := json.Unmarshal(out, &events); err != nil {
		return nil
	}
	return events.Items
}
