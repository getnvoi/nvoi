package kube

import (
	"context"
	"encoding/json"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// PodInfo is a simplified pod status for the wait-all-services loop.
type PodInfo struct {
	Name   string
	Ready  bool
	Status string // "Running", "CrashLoopBackOff", "ContainerCreating", etc.
}

// GetAllPods returns simplified status for all pods in a namespace.
func GetAllPods(ctx context.Context, ssh utils.SSHClient, ns string) ([]PodInfo, error) {
	out, err := ssh.Run(ctx, kubectl(ns, "get pods -o json"))
	if err != nil {
		return nil, err
	}

	var pods podList
	if err := json.Unmarshal(out, &pods); err != nil {
		return nil, err
	}

	var result []PodInfo
	for _, pod := range pods.Items {
		info := PodInfo{
			Name:   pod.Metadata.Name,
			Status: pod.Status.Phase,
		}

		allReady := len(pod.Status.ContainerStatuses) > 0
		for _, cs := range pod.Status.ContainerStatuses {
			if !cs.Ready {
				allReady = false
			}
			// Use the most specific status we can find.
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				info.Status = cs.State.Waiting.Reason
			}
			if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
				info.Status = cs.State.Terminated.Reason
			}
		}

		if allReady && pod.Status.Phase == "Running" {
			info.Ready = true
			info.Status = "Running"
		}

		result = append(result, info)
	}

	return result, nil
}
