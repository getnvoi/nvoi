package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Well-known tunnel agent workload names. Used by PurgeTunnelAgents and
// by pkg/provider/tunnel/{cloudflare,ngrok}/workloads.go (must stay in sync).
const (
	CloudflareTunnelAgentName  = "cloudflared"
	CloudflareTunnelSecretName = "cloudflared-token"
	NgrokTunnelAgentName       = "ngrok"
	NgrokTunnelSecretName      = "ngrok-authtoken"
	NgrokTunnelConfigMapName   = "ngrok-config"
)

// PurgeTunnelAgents deletes all known tunnel agent workloads (cloudflared and
// ngrok Deployment + Secret + ConfigMap) from the given namespace.
// Idempotent — NotFound on any resource is success.
// Called by the Caddy reconcile path when migrating back from a tunnel.
func (c *Client) PurgeTunnelAgents(ctx context.Context, ns string) error {
	// cloudflared Deployment + Secret.
	if err := IgnoreNotFound(c.cs.AppsV1().Deployments(ns).Delete(ctx, CloudflareTunnelAgentName, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("purge deployment/%s: %w", CloudflareTunnelAgentName, err)
	}
	if err := IgnoreNotFound(c.cs.CoreV1().Secrets(ns).Delete(ctx, CloudflareTunnelSecretName, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("purge secret/%s: %w", CloudflareTunnelSecretName, err)
	}
	// ngrok Deployment + Secret + ConfigMap.
	if err := IgnoreNotFound(c.cs.AppsV1().Deployments(ns).Delete(ctx, NgrokTunnelAgentName, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("purge deployment/%s: %w", NgrokTunnelAgentName, err)
	}
	if err := IgnoreNotFound(c.cs.CoreV1().Secrets(ns).Delete(ctx, NgrokTunnelSecretName, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("purge secret/%s: %w", NgrokTunnelSecretName, err)
	}
	if err := IgnoreNotFound(c.cs.CoreV1().ConfigMaps(ns).Delete(ctx, NgrokTunnelConfigMapName, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("purge configmap/%s: %w", NgrokTunnelConfigMapName, err)
	}
	return nil
}
