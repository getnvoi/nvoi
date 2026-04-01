package kube

import (
	"bytes"
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/core"
)

var kubeconfigPath = fmt.Sprintf("/home/%s/.kube/config", core.DefaultUser)

func kubectl(ns, command string) string {
	return fmt.Sprintf("KUBECONFIG=%s kubectl -n %s %s", kubeconfigPath, ns, command)
}

// Apply uploads a YAML manifest and runs kubectl apply.
func Apply(ctx context.Context, ssh core.SSHClient, ns string, yaml string) error {
	if err := ssh.Upload(ctx, bytes.NewReader([]byte(yaml)), core.KubeManifestPath(), 0o644); err != nil {
		return fmt.Errorf("upload manifest: %w", err)
	}
	out, err := ssh.Run(ctx, kubectl(ns, fmt.Sprintf("apply -f %s", core.KubeManifestPath())))
	if err != nil {
		return fmt.Errorf("kubectl apply: %s: %w", string(out), err)
	}
	return nil
}

// DeleteByName removes a workload + service by name. Tries both deployment and statefulset.
func DeleteByName(ctx context.Context, ssh core.SSHClient, ns, name string) error {
	ssh.Run(ctx, kubectl(ns, fmt.Sprintf("delete deployment/%s --ignore-not-found", name)))
	ssh.Run(ctx, kubectl(ns, fmt.Sprintf("delete statefulset/%s --ignore-not-found", name)))
	ssh.Run(ctx, kubectl(ns, fmt.Sprintf("delete service/%s --ignore-not-found", name)))
	return nil
}

// EnsureNamespace creates a namespace if it doesn't exist.
func EnsureNamespace(ctx context.Context, ssh core.SSHClient, ns string) error {
	cmd := fmt.Sprintf("KUBECONFIG=%s kubectl create namespace %s --dry-run=client -o yaml | KUBECONFIG=%s kubectl apply -f -",
		kubeconfigPath, ns, kubeconfigPath)
	if _, err := ssh.Run(ctx, cmd); err != nil {
		return fmt.Errorf("ensure namespace %s: %w", ns, err)
	}
	return nil
}
