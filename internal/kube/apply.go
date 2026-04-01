package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// ListSecretKeys returns the keys stored in a k8s Secret, or nil if the secret doesn't exist.
func ListSecretKeys(ctx context.Context, ssh core.SSHClient, ns, secretName string) ([]string, error) {
	cmd := kubectl(ns, fmt.Sprintf("get secret %s -o jsonpath='{.data}' 2>/dev/null", secretName))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("secret %q not found in namespace %q", secretName, ns)
	}

	// jsonpath output: '{"KEY1":"base64val","KEY2":"base64val"}'
	// We only need the keys.
	raw := strings.TrimSpace(string(out))
	raw = strings.Trim(raw, "'")
	if raw == "" || raw == "{}" {
		return nil, nil
	}

	var data map[string]string
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("parse secret keys: %w", err)
	}

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return keys, nil
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
