package reconcile

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Services(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig, packageEnvVars map[string]string, secretValues map[string]string) error {
	sources := mergeSources(packageEnvVars, secretValues)
	names, _ := dc.Cluster.Names()

	svcNames := utils.SortedKeys(cfg.Services)
	for _, name := range svcNames {
		svc := cfg.Services[name]
		image, err := resolveImageRef(ctx, dc, svc.Image, svc.Build)
		if err != nil {
			return err
		}
		servers := ResolveServers(cfg, svc.Servers, svc.Server, svc.Volumes)
		replicas := svc.Replicas
		if _, hasDomain := cfg.Domains[name]; hasDomain && replicas == 0 {
			replicas = 2
		}

		// Resolve env: entries — plain text in manifest
		plainEnv := make([]string, 0, len(svc.Env))
		for _, entry := range svc.Env {
			k, v, err := resolveEntry(entry, sources)
			if err != nil {
				return fmt.Errorf("services.%s.env: %w", name, err)
			}
			plainEnv = append(plainEnv, k+"="+v)
		}
		// Auto-inject package env vars (backward compat)
		for k, v := range packageEnvVars {
			plainEnv = append(plainEnv, k+"="+v)
		}

		// Resolve secrets: entries — stored in per-service k8s Secret
		svcSecretKVs, svcSecretRefs, err := resolveSecretEntries(name, svc.Secrets, sources)
		if err != nil {
			return err
		}

		// Upsert per-service secrets into k8s
		if err := upsertServiceSecrets(ctx, dc, names, name, svcSecretKVs); err != nil {
			return err
		}

		if err := app.ServiceSet(ctx, app.ServiceSetRequest{
			Cluster: dc.Cluster, Name: name, Image: image,
			Port: svc.Port, Command: svc.Command, Replicas: replicas,
			EnvVars: plainEnv, Storages: svc.Storage,
			SvcSecrets: svcSecretRefs,
			Volumes:    svc.Volumes, HealthPath: svc.Health, Servers: servers,
		}); err != nil {
			return err
		}
		kind := "deployment"
		if len(svc.Volumes) > 0 {
			kind = "statefulset"
		}
		if err := app.WaitRollout(ctx, app.WaitRolloutRequest{
			Cluster: dc.Cluster, Service: name,
			WorkloadKind: kind, HasHealthCheck: svc.Health != "",
		}); err != nil {
			return err
		}
	}

	if live != nil {
		desired := toSet(svcNames)
		// Exclude package-managed services from orphan detection
		protected := map[string]bool{}
		for dbName := range cfg.Database {
			protected[dbName+"-db"] = true
		}
		for _, name := range live.Services {
			if !desired[name] && !protected[name] {
				if err := app.ServiceDelete(ctx, app.ServiceDeleteRequest{Cluster: dc.Cluster, Name: name}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan service %s not removed: %s", name, err))
				}
				// Delete the orphan's per-service secret
				deleteServiceSecret(ctx, dc, names, name)
			}
		}
	}
	return nil
}

// resolveSecretEntries processes a service/cron's secrets: field.
// Convention: bare name "FOO" → shorthand for "FOO=$FOO".
// With "=", the right side MUST contain $ (enforced by validation).
// Returns the k8s Secret key-value pairs and the secretKeyRef strings.
func resolveSecretEntries(workload string, secrets []string, sources map[string]string) (kvs map[string]string, refs []string, err error) {
	kvs = make(map[string]string, len(secrets))
	refs = make([]string, 0, len(secrets))

	for _, entry := range secrets {
		// Normalize bare names: "FOO" → "FOO=$FOO"
		normalized := entry
		if !strings.Contains(entry, "=") {
			normalized = entry + "=$" + entry
		}

		envName, value, err := resolveEntry(normalized, sources)
		if err != nil {
			return nil, nil, fmt.Errorf("%s.secrets: %w", workload, err)
		}
		kvs[envName] = value
		refs = append(refs, envName)
	}
	return kvs, refs, nil
}

// upsertServiceSecrets creates/updates the per-service k8s Secret and
// orphan-removes keys that are no longer declared.
func upsertServiceSecrets(ctx context.Context, dc *config.DeployContext, names *utils.Names, svcName string, kvs map[string]string) error {
	if names == nil {
		return nil
	}
	ssh := dc.Cluster.MasterSSH
	if ssh == nil {
		return nil
	}
	ns := names.KubeNamespace()
	secretName := names.KubeServiceSecrets(svcName)

	if len(kvs) == 0 {
		// No secrets declared — delete the per-service secret if it exists
		return kube.DeleteSecret(ctx, ssh, ns, secretName)
	}

	// Upsert each key
	for key, val := range kvs {
		if err := kube.UpsertSecretKey(ctx, ssh, ns, secretName, key, val); err != nil {
			return fmt.Errorf("service %s secret %s: %w", svcName, key, err)
		}
	}

	// Orphan-remove keys no longer in the desired set
	existing, err := kube.ListSecretKeys(ctx, ssh, ns, secretName)
	if err != nil {
		return nil // secret may not exist yet on first deploy
	}
	desired := make(map[string]bool, len(kvs))
	for k := range kvs {
		desired[k] = true
	}
	for _, key := range existing {
		if !desired[key] {
			_ = kube.DeleteSecretKey(ctx, ssh, ns, secretName, key)
		}
	}
	return nil
}

// deleteServiceSecret removes a per-service k8s Secret entirely.
func deleteServiceSecret(ctx context.Context, dc *config.DeployContext, names *utils.Names, svcName string) {
	if names == nil {
		return
	}
	ssh := dc.Cluster.MasterSSH
	if ssh == nil {
		return
	}
	ns := names.KubeNamespace()
	secretName := names.KubeServiceSecrets(svcName)
	_ = kube.DeleteSecret(ctx, ssh, ns, secretName)
}

// mergeSources builds a unified lookup map for $VAR resolution.
func mergeSources(packageEnvVars, secretValues map[string]string) map[string]string {
	sources := make(map[string]string, len(packageEnvVars)+len(secretValues))
	for k, v := range secretValues {
		sources[k] = v
	}
	for k, v := range packageEnvVars {
		sources[k] = v
	}
	return sources
}
