package core

import (
	"context"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// SecretListRequest returns configured secret names.
// Config is the source of truth — no k8s scanning.
type SecretListRequest struct {
	SecretNames []string // from cfg.Secrets
}

func SecretList(_ context.Context, req SecretListRequest) ([]string, error) {
	return req.SecretNames, nil
}

// SecretRevealRequest reads a secret value from per-service k8s Secrets.
type SecretRevealRequest struct {
	Cluster
	Cfg          provider.ProviderConfigView
	Key          string
	ServiceNames []string // services/crons that might hold this key
}

func SecretReveal(ctx context.Context, req SecretRevealRequest) (string, error) {
	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if err != nil {
		return "", err
	}
	defer cleanup()

	ns := names.KubeNamespace()

	// Search per-service secrets for the key
	for _, svc := range req.ServiceNames {
		secretName := names.KubeServiceSecrets(svc)
		val, err := kc.GetSecretValue(ctx, ns, secretName, req.Key)
		if err == nil && val != "" {
			return val, nil
		}
	}

	// Fall back to global secret for legacy clusters still migrating
	val, err := kc.GetSecretValue(ctx, ns, names.KubeSecrets(), req.Key)
	if err == nil && val != "" {
		return val, nil
	}

	return "", ErrNotReady(req.Key + " not found in any service secret")
}
