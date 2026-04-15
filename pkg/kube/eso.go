package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── ESO + Reloader installation ─────────────────────────────────────────────

// EnsureESO installs External Secrets Operator via k3s HelmChart CRD.
// Idempotent — skips if already installed and ready.
func EnsureESO(ctx context.Context, ssh utils.SSHClient) error {
	// Check if ESO deployment is already ready.
	out, _ := ssh.Run(ctx, kctl("external-secrets", "get deploy external-secrets -o jsonpath='{.status.readyReplicas}' 2>/dev/null"))
	if strings.TrimSpace(strings.Trim(string(out), "'")) != "" {
		return nil // already running
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

// waitForESOReady polls until the ESO deployment is ready.
func waitForESOReady(ctx context.Context, ssh utils.SSHClient) error {
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
		return nil // already running
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

// Timing for ESO/Reloader readiness polling.
var esoPollInterval = 3 * time.Second
var esoTimeout = 3 * time.Minute

// ── SecretStore CRD ─────────────────────────────────────────────────────────

// SecretStoreSpec describes the ESO SecretStore to create.
type SecretStoreSpec struct {
	Name     string // SecretStore name (e.g. "nvoi-secrets")
	Kind     string // awssm, scaleway, doppler, infisical
	AuthName string // k8s Secret name holding the bootstrap token
}

// ApplySecretStore generates and applies a SecretStore CRD.
func ApplySecretStore(ctx context.Context, ssh utils.SSHClient, ns string, spec SecretStoreSpec) error {
	yaml, err := GenerateSecretStoreYAML(spec, ns)
	if err != nil {
		return err
	}
	return Apply(ctx, ssh, ns, yaml)
}

// GenerateSecretStoreYAML produces a SecretStore CRD for the given provider.
func GenerateSecretStoreYAML(spec SecretStoreSpec, ns string) (string, error) {
	var providerBlock string
	switch spec.Kind {
	case "awssm":
		providerBlock = fmt.Sprintf(`  provider:
    aws:
      service: SecretsManager
      auth:
        secretRef:
          accessKeyIDSecretRef:
            name: %s
            key: access_key_id
          secretAccessKeySecretRef:
            name: %s
            key: secret_access_key
      region: "%s"`, spec.AuthName, spec.AuthName, "us-east-1") // region injected at call site via bootstrap secret
	case "scaleway":
		providerBlock = fmt.Sprintf(`  provider:
    scaleway:
      accessKey:
        secretRef:
          name: %s
          key: access_key
      secretKey:
        secretRef:
          name: %s
          key: secret_key
      projectId:
        secretRef:
          name: %s
          key: project_id
      region: fr-par`, spec.AuthName, spec.AuthName, spec.AuthName)
	case "doppler":
		providerBlock = fmt.Sprintf(`  provider:
    doppler:
      auth:
        secretRef:
          dopplerToken:
            name: %s
            key: token`, spec.AuthName)
	case "infisical":
		providerBlock = fmt.Sprintf(`  provider:
    infisical:
      auth:
        universalAuthCredentials:
          serviceToken:
            secretRef:
              name: %s
              key: token`, spec.AuthName)
	default:
		return "", fmt.Errorf("unsupported ESO provider kind: %q", spec.Kind)
	}

	yaml := fmt.Sprintf(`apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: %s
  namespace: %s
spec:
%s`, spec.Name, ns, providerBlock)

	return yaml, nil
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
func GenerateExternalSecretYAML(spec ExternalSecretSpec, ns string) string {
	var dataEntries []string
	for _, key := range spec.Keys {
		dataEntries = append(dataEntries, fmt.Sprintf(`  - secretKey: %s
    remoteRef:
      key: %s`, key, key))
	}

	return fmt.Sprintf(`apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: %s
  namespace: %s
  labels:
    %s: %s
spec:
  refreshInterval: %s
  secretStoreRef:
    name: %s
    kind: SecretStore
  target:
    name: %s
    creationPolicy: Owner
  data:
%s`, spec.Name, ns,
		utils.LabelAppManagedBy, utils.LabelManagedBy,
		spec.RefreshInterval, spec.StoreName, spec.Name,
		strings.Join(dataEntries, "\n"))
}

// ApplyExternalSecret generates and applies an ExternalSecret CRD.
func ApplyExternalSecret(ctx context.Context, ssh utils.SSHClient, ns string, spec ExternalSecretSpec) error {
	yaml := GenerateExternalSecretYAML(spec, ns)
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
