package kube

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
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

// UpsertSecretKey adds or updates a single key in a k8s Secret.
// Creates the secret if it doesn't exist. Idempotent.
func UpsertSecretKey(ctx context.Context, ssh core.SSHClient, ns, secretName, key, value string) error {
	// kubectl create secret generic handles create-or-patch via dry-run + apply.
	// But it replaces the whole secret. We need to merge with existing keys.
	// Strategy: patch if exists, create if not.

	// Check if secret exists
	_, err := ssh.Run(ctx, kubectl(ns, fmt.Sprintf("get secret %s 2>/dev/null", secretName)))
	if err != nil {
		// Secret doesn't exist — create it
		cmd := kubectl(ns, fmt.Sprintf(
			"create secret generic %s --from-literal=%s=%s",
			secretName, shellQuote(key), shellQuote(value),
		))
		out, err := ssh.Run(ctx, cmd)
		if err != nil {
			return fmt.Errorf("create secret: %s: %w", string(out), err)
		}
		return nil
	}

	// Secret exists — patch the key
	cmd := kubectl(ns, fmt.Sprintf(
		"patch secret %s -p '{\"stringData\":{\"%s\":\"%s\"}}'",
		secretName, key, escapeJSON(value),
	))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("patch secret: %s: %w", string(out), err)
	}
	return nil
}

// DeleteSecretKey removes a single key from a k8s Secret.
func DeleteSecretKey(ctx context.Context, ssh core.SSHClient, ns, secretName, key string) error {
	cmd := kubectl(ns, fmt.Sprintf(
		"patch secret %s --type=json -p '[{\"op\":\"remove\",\"path\":\"/data/%s\"}]'",
		secretName, key,
	))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("delete secret key %s: %s: %w", key, string(out), err)
	}
	return nil
}

// GetSecretValue returns the decoded value of a single key from a k8s Secret.
func GetSecretValue(ctx context.Context, ssh core.SSHClient, ns, secretName, key string) (string, error) {
	cmd := kubectl(ns, fmt.Sprintf(
		"get secret %s -o jsonpath='{.data.%s}'", secretName, key,
	))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("secret key %q not found", key)
	}

	raw := strings.TrimSpace(string(out))
	raw = strings.Trim(raw, "'")
	if raw == "" {
		return "", fmt.Errorf("secret key %q not found or empty", key)
	}

	// Decode base64
	decoded, err := base64Decode(raw)
	if err != nil {
		return "", fmt.Errorf("decode secret %q: %w", key, err)
	}
	return decoded, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// LabelNode labels a k8s node with nvoi-role={role}. Idempotent — runs every deploy.
// Connects to master via SSH since kubectl lives there.
func LabelNode(ctx context.Context, masterIP string, sshKey []byte, nodeName, role string) error {
	ssh, err := infra.ConnectSSH(ctx, masterIP+":22", core.DefaultUser, sshKey)
	if err != nil {
		return fmt.Errorf("ssh master for node label: %w", err)
	}
	defer ssh.Close()

	cmd := fmt.Sprintf("KUBECONFIG=%s kubectl label node %s %s=%s --overwrite",
		kubeconfigPath, nodeName, core.LabelNvoiRole, role)
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("label node %s: %s: %w", nodeName, string(out), err)
	}
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
