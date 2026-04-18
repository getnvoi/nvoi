package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
)

// Registries reconciles the cluster's image-pull credentials.
//
// When cfg.Registry is non-empty:
//
//  1. Resolve every `$VAR` reference inside username/password against
//     dc.Creds (the same CredentialSource used by `secrets:` and storage).
//     Missing env var → hard error with a readable message.
//  2. Render a Docker config JSON from the resolved creds.
//  3. Apply it as a typed `kubernetes.io/dockerconfigjson` Secret named
//     `kube.PullSecretName` ("registry-auth") in the app namespace.
//
// When cfg.Registry is empty:
//
//   - Delete any pre-existing pull secret in the app namespace so removing
//     `registry:` from nvoi.yaml actually scrubs the cluster (no orphan
//     creds lingering).
//
// Apply happens before Services / Crons so kubelet can read the secret on
// the first pod's first image pull. The secret is namespaced — same
// namespace as the workloads — so kubelet's standard imagePullSecrets
// lookup just works without any cluster-wide RBAC dance.
func Registries(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	out := dc.Cluster.Log()
	names, err := dc.Cluster.Names()
	if err != nil {
		return err
	}
	ns := names.KubeNamespace()
	kc := dc.Cluster.MasterKube
	if kc == nil {
		return fmt.Errorf("registries: no master kube client")
	}

	// Empty registry block → orphan-clean any prior pull secret and stop.
	if len(cfg.Registry) == 0 {
		if err := kc.DeleteSecret(ctx, ns, kube.PullSecretName); err != nil {
			// DeleteSecret is already idempotent on NotFound; surface
			// anything else (transient API errors etc.).
			return fmt.Errorf("orphan pull secret: %w", err)
		}
		return nil
	}

	out.Command("registry", "set", fmt.Sprintf("%d host(s)", len(cfg.Registry)))

	resolved, err := resolveRegistries(dc, cfg)
	if err != nil {
		return err
	}

	secret, err := kube.BuildPullSecret(ns, resolved)
	if err != nil {
		return fmt.Errorf("build pull secret: %w", err)
	}
	if err := kc.Apply(ctx, ns, secret); err != nil {
		return fmt.Errorf("apply pull secret: %w", err)
	}
	out.Success(fmt.Sprintf("pull secret %s/%s applied", ns, kube.PullSecretName))
	return nil
}

// resolveRegistries expands every `$VAR` reference in cfg.Registry's
// username/password fields against dc.Creds and returns ready-to-encode
// auths. Missing references error out with the registry host + var name
// so the operator knows which credential to set.
func resolveRegistries(dc *config.DeployContext, cfg *config.AppConfig) (map[string]kube.RegistryAuth, error) {
	if dc.Creds == nil {
		return nil, fmt.Errorf("registries: no credential source")
	}

	out := make(map[string]kube.RegistryAuth, len(cfg.Registry))
	for host, def := range cfg.Registry {
		user, err := resolveRegistryField(dc, host, "username", def.Username)
		if err != nil {
			return nil, err
		}
		pass, err := resolveRegistryField(dc, host, "password", def.Password)
		if err != nil {
			return nil, err
		}
		if user == "" {
			return nil, fmt.Errorf("registry %s: username resolved empty", host)
		}
		if pass == "" {
			return nil, fmt.Errorf("registry %s: password resolved empty", host)
		}
		out[host] = kube.RegistryAuth{Username: user, Password: pass}
	}
	return out, nil
}

// resolveRegistryField resolves a single username/password value. If the
// raw value contains `$VAR` references, every referenced variable is
// fetched via dc.Creds.Get and substituted. Plain literals pass through.
func resolveRegistryField(dc *config.DeployContext, host, field, raw string) (string, error) {
	if !hasVarRef(raw) {
		return raw, nil
	}
	refs := extractVarRefs(raw)
	sources := make(map[string]string, len(refs))
	for _, name := range refs {
		val, err := dc.Creds.Get(name)
		if err != nil {
			return "", fmt.Errorf("registry %s.%s: lookup $%s: %w", host, field, name, err)
		}
		if val == "" {
			return "", fmt.Errorf("registry %s.%s: $%s is not set", host, field, name)
		}
		sources[name] = val
	}
	return resolveRef(raw, sources)
}
