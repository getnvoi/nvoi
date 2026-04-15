package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── ESO + Reloader installation ─────────────────────────────────────────────

// EnsureESO installs External Secrets Operator via k3s HelmChart CRD.
// Idempotent — skips if already installed and ready.
func EnsureESO(ctx context.Context, ssh utils.SSHClient) error {
	out, _ := ssh.Run(ctx, kctl("external-secrets", "get deploy external-secrets -o jsonpath='{.status.readyReplicas}' 2>/dev/null"))
	if strings.TrimSpace(strings.Trim(string(out), "'")) != "" {
		return nil
	}

	yaml := `apiVersion: helm.cattle.io/v1
kind: HelmChart
metadata:
  name: external-secrets
  namespace: kube-system
spec:
  repo: https://charts.external-secrets.io
  chart: external-secrets
  targetNamespace: external-secrets
  createNamespace: true
  set:
    installCRDs: "true"`

	if err := ApplyGlobal(ctx, ssh, yaml); err != nil {
		return fmt.Errorf("install ESO: %w", err)
	}
	return waitForESOReady(ctx, ssh)
}

func waitForESOReady(ctx context.Context, ssh utils.SSHClient) error {
	// Wait for the CRDs to be registered — the deployment can be ready
	// before the Helm controller finishes installing CRDs.
	if err := utils.Poll(ctx, esoPollInterval, esoTimeout, func() (bool, error) {
		_, err := ssh.Run(ctx, kctl("", "get crd secretstores.external-secrets.io 2>/dev/null"))
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("ESO CRDs not ready: %w", err)
	}

	// Then wait for the operator deployment.
	return utils.Poll(ctx, esoPollInterval, esoTimeout, func() (bool, error) {
		out, err := ssh.Run(ctx, kctl("external-secrets", "get deploy external-secrets -o jsonpath='{.status.readyReplicas}/{.spec.replicas}' 2>/dev/null"))
		if err != nil {
			return false, nil
		}
		parts := strings.Trim(string(out), "'")
		if parts == "" || !strings.Contains(parts, "/") {
			return false, nil
		}
		sides := strings.SplitN(parts, "/", 2)
		return sides[0] == sides[1] && sides[0] != "" && sides[0] != "0", nil
	})
}

// EnsureReloader installs Stakater Reloader via k3s HelmChart CRD.
// Idempotent — skips if already installed and ready.
func EnsureReloader(ctx context.Context, ssh utils.SSHClient) error {
	out, _ := ssh.Run(ctx, kctl("reloader", "get deploy reloader-reloader -o jsonpath='{.status.readyReplicas}' 2>/dev/null"))
	if strings.TrimSpace(strings.Trim(string(out), "'")) != "" {
		return nil
	}

	yaml := `apiVersion: helm.cattle.io/v1
kind: HelmChart
metadata:
  name: reloader
  namespace: kube-system
spec:
  repo: https://stakater.github.io/stakater-charts
  chart: reloader
  targetNamespace: reloader
  createNamespace: true`

	if err := ApplyGlobal(ctx, ssh, yaml); err != nil {
		return fmt.Errorf("install Reloader: %w", err)
	}
	return utils.Poll(ctx, esoPollInterval, esoTimeout, func() (bool, error) {
		out, err := ssh.Run(ctx, kctl("reloader", "get deploy reloader-reloader -o jsonpath='{.status.readyReplicas}' 2>/dev/null"))
		if err != nil {
			return false, nil
		}
		return strings.TrimSpace(strings.Trim(string(out), "'")) != "", nil
	})
}

var esoPollInterval = 3 * time.Second
var esoTimeout = 3 * time.Minute

// ── SecretStore CRD ─────────────────────────────────────────────────────────

// ESOProviderSpec is a function that returns the ESO provider block for a SecretStore.
// authName is the k8s Secret holding bootstrap credentials.
// creds are the resolved provider credentials (from env or DB).
// Returns the "provider" value for the SecretStore spec.
type ESOProviderSpec func(authName string, creds map[string]string) map[string]any

// esoProviderRegistry holds registered ESO provider specs.
// Populated by init() in each secrets provider package.
var esoProviderRegistry = map[string]ESOProviderSpec{}

// RegisterESOProvider registers an ESO provider spec factory.
func RegisterESOProvider(kind string, fn ESOProviderSpec) {
	esoProviderRegistry[kind] = fn
}

// ApplySecretStore generates and applies a SecretStore CRD.
func ApplySecretStore(ctx context.Context, ssh utils.SSHClient, ns, name, kind, authName string, creds map[string]string) error {
	yaml, err := GenerateSecretStoreYAML(name, ns, kind, authName, creds)
	if err != nil {
		return err
	}
	return Apply(ctx, ssh, ns, yaml)
}

// GenerateSecretStoreYAML produces a SecretStore CRD using the registered provider spec.
func GenerateSecretStoreYAML(name, ns, kind, authName string, creds map[string]string) (string, error) {
	fn, ok := esoProviderRegistry[kind]
	if !ok {
		return "", fmt.Errorf("unsupported ESO provider: %q", kind)
	}

	store := map[string]any{
		"apiVersion": "external-secrets.io/v1beta1",
		"kind":       "SecretStore",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"spec": map[string]any{
			"provider": fn(authName, creds),
		},
	}

	b, err := sigsyaml.Marshal(store)
	if err != nil {
		return "", fmt.Errorf("marshal SecretStore: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// ── ExternalSecret CRD ──────────────────────────────────────────────────────

// ExternalSecretSpec describes an ESO ExternalSecret to create.
type ExternalSecretSpec struct {
	Name            string   // ExternalSecret name (= target k8s Secret name)
	StoreName       string   // SecretStore name to reference
	Keys            []string // secret keys to sync from the external store
	RefreshInterval string   // e.g. "1h" or "0" for static
}

// GenerateExternalSecretYAML produces an ExternalSecret CRD.
func GenerateExternalSecretYAML(spec ExternalSecretSpec, ns string) (string, error) {
	data := make([]map[string]any, len(spec.Keys))
	for i, key := range spec.Keys {
		data[i] = map[string]any{
			"secretKey": key,
			"remoteRef": map[string]any{"key": key},
		}
	}

	es := map[string]any{
		"apiVersion": "external-secrets.io/v1beta1",
		"kind":       "ExternalSecret",
		"metadata": map[string]any{
			"name":      spec.Name,
			"namespace": ns,
			"labels": map[string]string{
				utils.LabelAppManagedBy: utils.LabelManagedBy,
			},
		},
		"spec": map[string]any{
			"refreshInterval": spec.RefreshInterval,
			"secretStoreRef": map[string]any{
				"name": spec.StoreName,
				"kind": "SecretStore",
			},
			"target": map[string]any{
				"name":           spec.Name,
				"creationPolicy": "Owner",
			},
			"data": data,
		},
	}

	b, err := sigsyaml.Marshal(es)
	if err != nil {
		return "", fmt.Errorf("marshal ExternalSecret: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// ApplyExternalSecret generates and applies an ExternalSecret CRD.
func ApplyExternalSecret(ctx context.Context, ssh utils.SSHClient, ns string, spec ExternalSecretSpec) error {
	yaml, err := GenerateExternalSecretYAML(spec, ns)
	if err != nil {
		return err
	}
	return Apply(ctx, ssh, ns, yaml)
}

// DeleteExternalSecret removes an ExternalSecret by name.
func DeleteExternalSecret(ctx context.Context, ssh utils.SSHClient, ns, name string) error {
	_, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("delete externalsecret %s --ignore-not-found", name)))
	if err != nil {
		return fmt.Errorf("delete externalsecret %s: %w", name, err)
	}
	return nil
}
