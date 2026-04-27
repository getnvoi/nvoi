package reconcile

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Services(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, sources map[string]string) error {
	names, _ := dc.Cluster.Names()

	// Pull-secret name only when the user declared private registries.
	// Empty string → BuildService skips imagePullSecrets entirely; pods
	// pull anonymously (works for public images on Docker Hub/ghcr/etc.).
	pullSecret := ""
	if len(cfg.Registry) > 0 {
		pullSecret = kube.PullSecretName
	}

	// Services are applied + waited in dependency order: any service listed
	// in another's depends_on is fully Ready before its dependents are
	// applied. Eliminates DNS-not-yet-registered races at pod startup.
	svcNames := topoSortServices(cfg.Services)
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
		expandDatabaseCreds(svc.Databases, sources, svcSecretKVs, &svcSecretRefs)

		// Upsert per-service secrets into k8s
		if err := upsertServiceSecrets(ctx, dc, names, name, svcSecretKVs); err != nil {
			return err
		}

		// ResolveImage applies the Kamal-style convention: built services
		// get their image rewritten to <host>/<repo>[:user-tag]-<hash>;
		// pull-only services pass through unchanged.
		resolvedImage, err := ResolveImage(cfg, name, dc.Cluster.DeployHash)
		if err != nil {
			return err
		}
		if err := app.ServiceSet(ctx, app.ServiceSetRequest{
			Cluster: dc.Cluster, Cfg: config.NewView(cfg),
			Name: name, Image: resolvedImage,
			Port: svc.Port, Command: svc.Command, Replicas: replicas,
			EnvVars:    plainEnv,
			SvcSecrets: svcSecretRefs,
			Volumes:    svc.Volumes, HealthPath: svc.Health, Servers: servers,
			PullSecretName: pullSecret,
			KnownVolumes:   knownVolumes(cfg),
		}); err != nil {
			return err
		}
		kind := "deployment"
		if len(svc.Volumes) > 0 {
			kind = "statefulset"
		}
		if err := app.WaitRollout(ctx, app.WaitRolloutRequest{
			Cluster: dc.Cluster, Cfg: config.NewView(cfg),
			Service:      name,
			WorkloadKind: kind, HasHealthCheck: svc.Health != "",
		}); err != nil {
			return err
		}
	}

	// Orphan sweep — every nvoi-owned workload, k8s Service, and per-
	// service Secret in the namespace whose owner is `services` and
	// whose name isn't in `desired` gets deleted. The owner-scoped
	// listing means this can never see databases / tunnel / caddy /
	// registries resources, so no exclusion logic is needed.
	ns := names.KubeNamespace()
	kc := dc.Cluster.MasterKube
	desiredSecrets := make([]string, 0, len(svcNames))
	for _, n := range svcNames {
		desiredSecrets = append(desiredSecrets, names.KubeServiceSecrets(n))
	}
	for _, sweep := range []struct {
		kind    kube.Kind
		desired []string
	}{
		{kube.KindDeployment, svcNames},
		{kube.KindStatefulSet, svcNames},
		{kube.KindService, svcNames},
		{kube.KindSecret, desiredSecrets},
	} {
		if err := provider.SweepOwned(ctx, kc, ns, provider.KindServiceWorkload, sweep.kind, sweep.desired); err != nil {
			dc.Cluster.Log().Warning(fmt.Sprintf("services sweep %s: %s", sweep.kind, err))
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

func expandDatabaseCreds(databaseRefs []string, sources map[string]string, kvs map[string]string, refs *[]string) {
	for _, ref := range databaseRefs {
		envName, dbName := parseDatabaseRef(ref)
		if envName == "" {
			envName = utils.DatabaseEnvName(dbName)
		}
		if val, ok := sources[utils.DatabaseEnvName(dbName)]; ok {
			kvs[envName] = val
			*refs = append(*refs, envName)
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

	if err := provider.EnsureSecret(ctx, kc, ns, provider.KindServiceWorkload, secretName, kvs); err != nil {
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
