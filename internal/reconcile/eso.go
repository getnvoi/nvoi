package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
)

const (
	esoStoreName     = "nvoi-secrets"
	esoBootstrapName = "nvoi-eso-auth"
)

// ESOSetup installs ESO + Reloader and configures the SecretStore.
// No-op if no secrets provider is configured (explicit or implied).
func ESOSetup(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	kind := cfg.Providers.Secrets
	if kind == "" {
		return nil // no secrets provider — baseline mode
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

	// 2. Install Reloader (auto-restart pods on secret change)
	dc.Cluster.Log().Info("installing Reloader...")
	if err := kube.EnsureReloader(ctx, ssh); err != nil {
		return fmt.Errorf("eso: install reloader: %w", err)
	}
	dc.Cluster.Log().Success("Reloader installed")

	// 3. Create bootstrap secret (the one credential ESO needs to authenticate)
	dc.Cluster.Log().Info("configuring SecretStore...")
	if err := createBootstrapSecret(ctx, dc, cfg, kind, ns); err != nil {
		return fmt.Errorf("eso: bootstrap secret: %w", err)
	}

	// 4. Apply SecretStore CRD
	if err := kube.ApplySecretStore(ctx, ssh, ns, kube.SecretStoreSpec{
		Name:     esoStoreName,
		Kind:     kind,
		AuthName: esoBootstrapName,
	}); err != nil {
		return fmt.Errorf("eso: apply SecretStore: %w", err)
	}
	dc.Cluster.Log().Success("SecretStore configured")

	return nil
}

// createBootstrapSecret writes the secrets provider's own credentials
// as a k8s Secret. For implied providers (aws, scaleway), this uses
// the compute provider's credentials. For explicit providers (doppler,
// infisical), this uses the secrets provider's resolved credentials.
func createBootstrapSecret(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, kind, ns string) error {
	ssh := dc.Cluster.MasterSSH

	switch kind {
	case "awssm":
		// Compute creds double as secrets backend creds
		creds := dc.Cluster.Credentials
		if creds == nil {
			return fmt.Errorf("aws compute credentials required for implied secrets provider")
		}
		for _, kv := range []struct{ k, v string }{
			{"access_key_id", creds["access_key_id"]},
			{"secret_access_key", creds["secret_access_key"]},
			{"region", creds["region"]},
		} {
			if kv.v == "" {
				continue
			}
			if err := kube.UpsertSecretKey(ctx, ssh, ns, esoBootstrapName, kv.k, kv.v); err != nil {
				return err
			}
		}

	case "scaleway":
		creds := dc.Cluster.Credentials
		if creds == nil {
			return fmt.Errorf("scaleway compute credentials required for implied secrets provider")
		}
		for _, kv := range []struct{ k, v string }{
			{"access_key", creds["access_key"]},
			{"secret_key", creds["secret_key"]},
			{"project_id", creds["project_id"]},
		} {
			if kv.v == "" {
				continue
			}
			if err := kube.UpsertSecretKey(ctx, ssh, ns, esoBootstrapName, kv.k, kv.v); err != nil {
				return err
			}
		}

	case "doppler":
		// Explicit secrets provider — creds resolved at DeployContext build time
		if token := dc.SecretsCreds["token"]; token != "" {
			if err := kube.UpsertSecretKey(ctx, ssh, ns, esoBootstrapName, "token", token); err != nil {
				return err
			}
		}

	case "infisical":
		if token := dc.SecretsCreds["token"]; token != "" {
			if err := kube.UpsertSecretKey(ctx, ssh, ns, esoBootstrapName, "token", token); err != nil {
				return err
			}
		}
	}

	return nil
}
