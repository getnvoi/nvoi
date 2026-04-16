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

func Services(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig, sources map[string]string) error {
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

		// Write secrets to cluster: ESO ExternalSecret or plaintext k8s Secret
		esoActive := cfg.Providers.Secrets != ""
		var svcSecretRefs []string
		if esoActive {
			// ESO path: collect key names only — ESO fetches values inside the cluster.
			svcSecretRefs = collectSecretKeyNames(svc.Secrets)
			expandStorageKeyNames(svc.Storage, &svcSecretRefs)
			if len(svcSecretRefs) > 0 {
				if err := upsertExternalSecret(ctx, dc, names, name, svcSecretRefs); err != nil {
					return err
				}
			}
		} else {
			// Baseline path: resolve values from sources, write plaintext k8s Secret
			var svcSecretKVs map[string]string
			var err error
			svcSecretKVs, svcSecretRefs, err = resolveSecretEntries(name, svc.Secrets, sources)
			if err != nil {
				return err
			}
			expandStorageCreds(svc.Storage, sources, svcSecretKVs, &svcSecretRefs)
			if err := upsertServiceSecrets(ctx, dc, names, name, svcSecretKVs); err != nil {
				return err
			}
		}

		if err := app.ServiceSet(ctx, app.ServiceSetRequest{
			Cluster: dc.Cluster, Name: name, Image: image,
			Port: svc.Port, Command: svc.Command, Replicas: replicas,
			EnvVars:    plainEnv,
			SvcSecrets: svcSecretRefs,
			Volumes:    svc.Volumes, HealthPath: svc.Health, Servers: servers,
			ESOManaged: esoActive,
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
		for _, db := range cfg.Database {
			protected[db.ServiceName] = true
		}
		for _, name := range live.Services {
			if !desired[name] && !protected[name] {
				if err := app.ServiceDelete(ctx, app.ServiceDeleteRequest{Cluster: dc.Cluster, Name: name}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan service %s not removed: %s", name, err))
				}
				deleteServiceSecret(ctx, dc, names, name)
			}
		}
	}
	return nil
}

// collectSecretKeyNames extracts the env var names from a secrets list
// without resolving values. Used by the ESO path where values come from
// the external store, not from local sources.
func collectSecretKeyNames(secrets []string) []string {
	refs := make([]string, 0, len(secrets))
	for _, entry := range secrets {
		envName, _ := kube.ParseSecretRef(entry)
		refs = append(refs, envName)
	}
	return refs
}

// expandStorageKeyNames adds storage credential key names to the refs list.
func expandStorageKeyNames(storageNames []string, refs *[]string) {
	for _, storageName := range storageNames {
		*refs = append(*refs, app.StorageSecretKeys(storageName)...)
	}
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

// upsertExternalSecret creates/updates an ESO ExternalSecret CRD for a service.
// ESO syncs the referenced keys from the SecretStore into a k8s Secret.
func upsertExternalSecret(ctx context.Context, dc *config.DeployContext, names *utils.Names, svcName string, refs []string) error {
	if names == nil {
		return nil
	}
	ssh := dc.Cluster.MasterSSH
	if ssh == nil {
		return nil
	}
	ns := names.KubeNamespace()
	secretName := names.KubeServiceSecrets(svcName)

	return kube.ApplyExternalSecret(ctx, ssh, ns, kube.ExternalSecretSpec{
		Name:            secretName,
		StoreName:       esoStoreName,
		Keys:            refs,
		RefreshInterval: "1h",
	})
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
