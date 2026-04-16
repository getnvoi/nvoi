// Package kube handles Kubernetes operations: client-go API access,
// YAML generation, ingress management, and rollout monitoring.
package kube

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// PodSelector returns a label selector for pods of a specific service.
func PodSelector(service string) string {
	return fmt.Sprintf("%s=%s", utils.LabelAppName, service)
}

// kctl builds a kubectl command string. Used only by LabelNode (bootstrap path).
func kctl(ns, command string) string {
	if ns != "" {
		return fmt.Sprintf("KUBECONFIG=%s kubectl -n %s %s", utils.UserKubeconfigPath, ns, command)
	}
	return fmt.Sprintf("KUBECONFIG=%s kubectl %s", utils.UserKubeconfigPath, command)
}

// LabelNode labels a k8s node via SSH kubectl. Used during bootstrap
// provisioning (ComputeSet) before KubeClient is established.
func LabelNode(ctx context.Context, ssh utils.SSHClient, nodeName, role string) error {
	cmd := kctl("", fmt.Sprintf("label node %s %s=%s --overwrite", nodeName, utils.LabelNvoiRole, role))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("label node %s: %s: %w", nodeName, string(out), err)
	}
	return nil
}
