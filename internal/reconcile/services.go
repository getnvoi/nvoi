package reconcile

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Services(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig, sources map[string]string) error {
	names, _ := dc.Cluster.Names()

	svcNames := utils.SortedKeys(cfg.Services)
	for _, name := range svcNames {
		svc := cfg.Services[name]
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

		// Resolve secrets: entries — stored in per-service k8s Secret
		svcSecretKVs, svcSecretRefs, err := resolveSecretEntries(name, svc.Secrets, sources)
		if err != nil {
			return err
		}

		// Expand storage: into per-service secret entries
		expandStorageCreds(svc.Storage, sources, svcSecretKVs, &svcSecretRefs)

		// Upsert per-service secrets into k8s
		if err := upsertServiceSecrets(ctx, dc, names, name, svcSecretKVs); err != nil {
			return err
		}

		if err := app.ServiceSet(ctx, app.ServiceSetRequest{
			Cluster: dc.Cluster, Name: name, Image: svc.Image,
			Port: svc.Port, Command: svc.Command, Replicas: replicas,
			EnvVars:    plainEnv,
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
		for _, name := range live.Services {
			if !desired[name] {
				if err := app.ServiceDelete(ctx, app.ServiceDeleteRequest{Cluster: dc.Cluster, Name: name}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan service %s not removed: %s", name, err))
				}
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

// expandStorageCreds adds storage credentials to the per-service secret
// for each storage bucket declared on the service/cron.
func expandStorageCreds(storageNames []string, sources map[string]string, kvs map[string]string, refs *[]string) {
	for _, storageName := range storageNames {
		for _, key := range app.StorageSecretKeys(storageName) {
			if val, ok := sources[key]; ok {
				kvs[key] = val
				*refs = append(*refs, key)
			}
		}
	}
}

// upsertServiceSecrets creates/updates the per-service k8s Secret and
// orphan-removes keys that are no longer declared.
func upsertServiceSecrets(ctx context.Context, dc *config.DeployContext, names *utils.Names, svcName string, kvs map[string]string) error {
	if names == nil {
		return nil
	}
	kc := dc.Cluster.MasterKube
	if kc == nil {
		return nil
	}
	ns := names.KubeNamespace()
	secretName := names.KubeServiceSecrets(svcName)

	if len(kvs) == 0 {
		return kc.DeleteSecret(ctx, ns, secretName)
	}

	if err := kc.EnsureSecret(ctx, ns, secretName, kvs); err != nil {
		return fmt.Errorf("service %s secret: %w", svcName, err)
	}

	existing, err := kc.ListSecretKeys(ctx, ns, secretName)
	if err != nil {
		return nil
	}
	desired := make(map[string]bool, len(kvs))
	for k := range kvs {
		desired[k] = true
	}
	for _, key := range existing {
		if !desired[key] {
			_ = kc.DeleteSecretKey(ctx, ns, secretName, key)
		}
	}
	return nil
}

// deleteServiceSecret removes a per-service k8s Secret entirely.
func deleteServiceSecret(ctx context.Context, dc *config.DeployContext, names *utils.Names, svcName string) {
	if names == nil {
		return
	}
	kc := dc.Cluster.MasterKube
	if kc == nil {
		return
	}
	_ = kc.DeleteSecret(ctx, names.KubeNamespace(), names.KubeServiceSecrets(svcName))
}

// mergeSources builds a unified lookup map for $VAR resolution.
func mergeSources(maps ...map[string]string) map[string]string {
	size := 0
	for _, m := range maps {
		size += len(m)
	}
	sources := make(map[string]string, size)
	for _, m := range maps {
		for k, v := range m {
			sources[k] = v
		}
	}
	return sources
}
