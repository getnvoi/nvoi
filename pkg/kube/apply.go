// Package kube handles Kubernetes YAML generation, kubectl operations over SSH, Ingress resource management, and rollout monitoring.
package kube

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
)

var kubeconfigPath = fmt.Sprintf("/home/%s/.kube/config", utils.DefaultUser)

// NvoiSelector is the label selector for nvoi-managed resources.
var NvoiSelector = fmt.Sprintf("%s=%s", utils.LabelAppManagedBy, utils.LabelManagedBy)

// kctl is the single source of truth for kubectl command strings.
// ns == "" → cluster-scoped. ns != "" → namespaced.
func kctl(ns, command string) string {
	if ns != "" {
		return fmt.Sprintf("KUBECONFIG=%s kubectl -n %s %s", kubeconfigPath, ns, command)
	}
	return fmt.Sprintf("KUBECONFIG=%s kubectl %s", kubeconfigPath, command)
}

// GetJSON runs a namespaced kubectl get and returns raw JSON bytes.
// selector is optional (e.g. NvoiSelector or "").
func GetJSON(ctx context.Context, ssh utils.SSHClient, ns, resource, selector string) ([]byte, error) {
	sel := ""
	if selector != "" {
		sel = " -l " + selector
	}
	return ssh.Run(ctx, kctl(ns, fmt.Sprintf("get %s%s -o json", resource, sel)))
}

// GetClusterJSON runs a cluster-wide (non-namespaced) kubectl get and returns raw JSON bytes.
func GetClusterJSON(ctx context.Context, ssh utils.SSHClient, resource string) ([]byte, error) {
	return GetJSON(ctx, ssh, "", resource, "")
}

// GetNamedJSON runs kubectl get on a specific named resource and returns raw JSON bytes.
func GetNamedJSON(ctx context.Context, ssh utils.SSHClient, ns, resource, name string) ([]byte, error) {
	return ssh.Run(ctx, kctl(ns, fmt.Sprintf("get %s %s -o json 2>/dev/null", resource, name)))
}

// RunKubectl runs an arbitrary kubectl command and returns output.
func RunKubectl(ctx context.Context, ssh utils.SSHClient, ns, command string) ([]byte, error) {
	return ssh.Run(ctx, kctl(ns, command))
}

// RunStream runs a kubectl command and streams stdout/stderr to the provided writers.
func RunStream(ctx context.Context, ssh utils.SSHClient, ns, command string, stdout, stderr io.Writer) error {
	return ssh.RunStream(ctx, kctl(ns, command), stdout, stderr)
}

// PodSelector returns the label selector for pods belonging to a service.
func PodSelector(service string) string {
	return fmt.Sprintf("%s=%s", utils.LabelAppName, service)
}

// FirstPod returns the name of the first running pod for a service.
func FirstPod(ctx context.Context, ssh utils.SSHClient, ns, service string) (string, error) {
	sel := PodSelector(service)
	out, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("get pods -l %s -o jsonpath='{.items[0].metadata.name}'", sel)))
	if err != nil {
		return "", fmt.Errorf("get pods for %q: %w", service, err)
	}
	pod := strings.Trim(strings.TrimSpace(string(out)), "'")
	if pod == "" {
		return "", fmt.Errorf("no pods found for service %q", service)
	}
	return pod, nil
}

// Apply uploads a YAML manifest and applies it with server-side apply.
// Server-side apply does field-level merge — rolling updates work correctly
// when nodeSelector or other fields change (new pods created before old ones die).
func Apply(ctx context.Context, ssh utils.SSHClient, ns string, yaml string) error {
	if err := ssh.Upload(ctx, bytes.NewReader([]byte(yaml)), utils.KubeManifestPath(), 0o644); err != nil {
		return fmt.Errorf("upload manifest: %w", err)
	}
	out, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("apply --server-side --force-conflicts -f %s", utils.KubeManifestPath())))
	if err != nil {
		return fmt.Errorf("kubectl apply: %s: %w", string(out), err)
	}
	return nil
}

// ApplyGlobal uploads a YAML manifest and applies it cluster-wide (no namespace).
func ApplyGlobal(ctx context.Context, ssh utils.SSHClient, yaml string) error {
	return Apply(ctx, ssh, "", yaml)
}

// DeleteByName removes a workload + service by name. Tries both deployment and statefulset.
// --ignore-not-found handles "already gone." SSH errors are real failures.
func DeleteByName(ctx context.Context, ssh utils.SSHClient, ns, name string) error {
	if _, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("delete deployment/%s --ignore-not-found", name))); err != nil {
		return fmt.Errorf("delete deployment/%s: %w", name, err)
	}
	if _, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("delete statefulset/%s --ignore-not-found", name))); err != nil {
		return fmt.Errorf("delete statefulset/%s: %w", name, err)
	}
	if _, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("delete service/%s --ignore-not-found", name))); err != nil {
		return fmt.Errorf("delete service/%s: %w", name, err)
	}
	return nil
}

// ListSecretKeys returns the keys stored in a k8s Secret, or nil if the secret doesn't exist.
func ListSecretKeys(ctx context.Context, ssh utils.SSHClient, ns, secretName string) ([]string, error) {
	cmd := kctl(ns, fmt.Sprintf("get secret %s -o jsonpath='{.data}' 2>/dev/null", secretName))
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
// Uses --from-literal for create (shellQuote handles special chars)
// and uploads a JSON patch file for update (avoids shell injection).
func UpsertSecretKey(ctx context.Context, ssh utils.SSHClient, ns, secretName, key, value string) error {
	// Check if secret exists
	_, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("get secret %s 2>/dev/null", secretName)))
	if err != nil {
		// Secret doesn't exist — create it
		cmd := kctl(ns, fmt.Sprintf(
			"create secret generic %s --from-literal=%s=%s",
			secretName, shellQuote(key), shellQuote(value),
		))
		out, err := ssh.Run(ctx, cmd)
		if err != nil {
			return fmt.Errorf("create secret: %s: %w", string(out), err)
		}
		return nil
	}

	// Secret exists — upload patch as file to avoid shell injection
	patch, err := json.Marshal(map[string]any{"stringData": map[string]string{key: value}})
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}
	patchPath := fmt.Sprintf("/home/%s/.nvoi-patch.json", utils.DefaultUser)
	if err := ssh.Upload(ctx, bytes.NewReader(patch), patchPath, 0o600); err != nil {
		return fmt.Errorf("upload patch: %w", err)
	}

	cmd := kctl(ns, fmt.Sprintf("patch secret %s --type=merge -p \"$(cat %s)\"", secretName, patchPath))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("patch secret: %s: %w", string(out), err)
	}
	return nil
}

// DeleteSecretKey removes a single key from a k8s Secret.
// Idempotent — succeeds if the key or secret doesn't exist.
func DeleteSecretKey(ctx context.Context, ssh utils.SSHClient, ns, secretName, key string) error {
	// Check if secret exists
	_, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("get secret %s 2>/dev/null", secretName)))
	if err != nil {
		return nil // secret doesn't exist — nothing to delete
	}

	// Check if key exists in the secret
	existing, err := ListSecretKeys(ctx, ssh, ns, secretName)
	if err != nil {
		return nil
	}
	found := false
	for _, k := range existing {
		if k == key {
			found = true
			break
		}
	}
	if !found {
		return nil // key doesn't exist — already deleted
	}

	cmd := kctl(ns, fmt.Sprintf(
		"patch secret %s --type=json -p '[{\"op\":\"remove\",\"path\":\"/data/%s\"}]'",
		secretName, key,
	))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("delete secret key %s: %s: %w", key, string(out), err)
	}
	return nil
}

// DeleteSecret removes an entire k8s Secret by name.
// Idempotent — succeeds if the secret doesn't exist.
func DeleteSecret(ctx context.Context, ssh utils.SSHClient, ns, secretName string) error {
	cmd := kctl(ns, fmt.Sprintf("delete secret %s --ignore-not-found", secretName))
	if _, err := ssh.Run(ctx, cmd); err != nil {
		return fmt.Errorf("delete secret %s: %w", secretName, err)
	}
	return nil
}

// GetSecretValue returns the decoded value of a single key from a k8s Secret.
func GetSecretValue(ctx context.Context, ssh utils.SSHClient, ns, secretName, key string) (string, error) {
	cmd := kctl(ns, fmt.Sprintf(
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

// DrainAndRemoveNode removes a node from the cluster.
// If the node is alive, drains it first (evicts pods gracefully).
// If the node is dead (NotReady), skips drain and force-removes it.
// Idempotent — succeeds if the node doesn't exist.
// DrainAndRemoveNode drains workloads from a node and removes it from the cluster.
// Returns nil if the node doesn't exist (already removed).
//
// Self-healing: if drain fails and the node is NotReady (dead/unreachable),
// force-removes the node — workloads are already gone, no data loss.
// If drain fails on a Ready node, returns error — workloads are running
// and can't be evicted, caller must NOT delete the server.
func DrainAndRemoveNode(ctx context.Context, ssh utils.SSHClient, nodeName string) error {
	// Check if node exists in the cluster.
	out, err := ssh.Run(ctx, kctl("", fmt.Sprintf("get node %s --no-headers 2>/dev/null", nodeName)))
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return nil // node not in cluster — nothing to drain
	}

	// Try drain.
	if _, drainErr := ssh.Run(ctx, kctl("", fmt.Sprintf(
		"drain %s --ignore-daemonsets --delete-emptydir-data --force --timeout=30s", nodeName))); drainErr != nil {

		// Drain failed — check if node is NotReady (dead/unreachable).
		statusOut, _ := ssh.Run(ctx, kctl("", fmt.Sprintf(
			"get node %s -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' 2>/dev/null", nodeName)))
		nodeReady := strings.Trim(strings.TrimSpace(string(statusOut)), "'") == "True"

		if nodeReady {
			// Node is alive but drain failed — workloads can't be evicted. Don't delete.
			return fmt.Errorf("drain node %s: %w", nodeName, drainErr)
		}

		// Node is NotReady — already dead. Force-remove from cluster.
	}

	// Remove the node from the cluster.
	if _, err := ssh.Run(ctx, kctl("", fmt.Sprintf("delete node %s --ignore-not-found", nodeName))); err != nil {
		return fmt.Errorf("delete node %s: %w", nodeName, err)
	}

	return nil
}

// LabelNode labels a k8s node with nvoi-role={role}. Idempotent — runs every deploy.
func LabelNode(ctx context.Context, ssh utils.SSHClient, nodeName, role string) error {
	cmd := kctl("", fmt.Sprintf("label node %s %s=%s --overwrite", nodeName, utils.LabelNvoiRole, role))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("label node %s: %s: %w", nodeName, string(out), err)
	}
	return nil
}

// GetServicePort returns the first port of a k8s Service, or error if not found.
func GetServicePort(ctx context.Context, ssh utils.SSHClient, ns, name string) (int, error) {
	cmd := kctl(ns, fmt.Sprintf("get service %s -o jsonpath='{.spec.ports[0].port}' 2>/dev/null", name))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return 0, fmt.Errorf("service %q not found", name)
	}
	raw := strings.TrimSpace(string(out))
	raw = strings.Trim(raw, "'")
	var port int
	if _, err := fmt.Sscanf(raw, "%d", &port); err != nil || port == 0 {
		return 0, fmt.Errorf("service %q has no port", name)
	}
	return port, nil
}

// EnsureNamespace creates a namespace if it doesn't exist.
func EnsureNamespace(ctx context.Context, ssh utils.SSHClient, ns string) error {
	cmd := kctl("", fmt.Sprintf("create namespace %s --dry-run=client -o yaml", ns)) +
		" | " + kctl("", "apply -f -")
	if _, err := ssh.Run(ctx, cmd); err != nil {
		return fmt.Errorf("ensure namespace %s: %w", ns, err)
	}
	return nil
}
