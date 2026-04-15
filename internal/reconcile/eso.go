package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

const (
	esoStoreName     = "nvoi-secrets"
	esoBootstrapName = "nvoi-eso-auth"
)

// ESOSetup installs ESO + Reloader and configures the SecretStore.
// No-op if no secrets provider is configured.
func ESOSetup(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	kind := cfg.Providers.Secrets
	if kind == "" {
		return nil
	}

	ssh := dc.Cluster.MasterSSH
	if ssh == nil {
		return nil
	}
	names, _ := dc.Cluster.Names()
	if names == nil {
		return nil
	}
	ns := names.KubeNamespace()

	dc.Cluster.Log().Command("eso", "setup", kind)

	// 1. Install ESO operator
	dc.Cluster.Log().Info("installing External Secrets Operator...")
	if err := kube.EnsureESO(ctx, ssh); err != nil {
		return fmt.Errorf("eso: install operator: %w", err)
	}
	dc.Cluster.Log().Success("ESO installed")

	// 2. Install Reloader
	dc.Cluster.Log().Info("installing Reloader...")
	if err := kube.EnsureReloader(ctx, ssh); err != nil {
		return fmt.Errorf("eso: install reloader: %w", err)
	}
	dc.Cluster.Log().Success("Reloader installed")

	// 3. Resolve credentials and create bootstrap secret
	dc.Cluster.Log().Info("configuring SecretStore...")
	creds := resolveESOCreds(dc, kind)

	schema, err := provider.GetSecretsSchema(kind)
	if err != nil {
		return fmt.Errorf("eso: unknown provider %q: %w", kind, err)
	}
	for _, f := range schema.Fields {
		if v := creds[f.Key]; v != "" {
			if err := kube.UpsertSecretKey(ctx, ssh, ns, esoBootstrapName, f.Key, v); err != nil {
				return fmt.Errorf("eso: bootstrap secret key %s: %w", f.Key, err)
			}
		}
	}

	// 4. Apply SecretStore CRD
	if err := kube.ApplySecretStore(ctx, ssh, ns, esoStoreName, kind, esoBootstrapName, creds); err != nil {
		return fmt.Errorf("eso: apply SecretStore: %w", err)
	}
	dc.Cluster.Log().Success("SecretStore configured")

	return nil
}

// resolveESOCreds returns the credentials for the ESO bootstrap secret.
// Uses SecretsCreds from DeployContext (resolved at build time from env/DB).
func resolveESOCreds(dc *config.DeployContext, kind string) map[string]string {
	if dc.SecretsCreds != nil && len(dc.SecretsCreds) > 0 {
		return dc.SecretsCreds
	}
	// Fallback: compute credentials (for implied providers like awssm/scaleway).
	return dc.Cluster.Credentials
}
