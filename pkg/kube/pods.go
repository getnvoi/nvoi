package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodInfo is a simplified pod status for the wait-all-services loop.
type PodInfo struct {
	Name   string
	Ready  bool
	Status string // "Running", "CrashLoopBackOff", "ContainerCreating", etc.
}

// GetAllPods returns simplified status for all pods in a namespace.
func (c *Client) GetAllPods(ctx context.Context, ns string) ([]PodInfo, error) {
	pods, err := c.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	result := make([]PodInfo, 0, len(pods.Items))
	for i := range pods.Items {
		result = append(result, summarizePod(&pods.Items[i]))
	}
	return result, nil
}

// summarizePod collapses a corev1.Pod's container statuses into a single
// {Ready, Status} pair using the most specific available reason.
func summarizePod(pod *corev1.Pod) PodInfo {
	info := PodInfo{
		Name:   pod.Name,
		Status: string(pod.Status.Phase),
	}

	allReady := len(pod.Status.ContainerStatuses) > 0
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			allReady = false
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			info.Status = cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			info.Status = cs.State.Terminated.Reason
		}
	}

	if allReady && pod.Status.Phase == corev1.PodRunning {
		info.Ready = true
		info.Status = "Running"
	}
	return info
}
