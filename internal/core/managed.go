package core

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/managed"
	"github.com/spf13/cobra"
)

// resolveCluster builds a Cluster from the standard CLI flags.
func resolveCluster(cmd *cobra.Command) (app.Cluster, error) {
	appName, env, err := resolveAppEnv(cmd)
	if err != nil {
		return app.Cluster{}, err
	}
	providerName, err := resolveComputeProvider(cmd)
	if err != nil {
		return app.Cluster{}, err
	}
	creds, err := resolveComputeCredentials(cmd, providerName)
	if err != nil {
		return app.Cluster{}, err
	}
	sshKey, err := resolveSSHKey()
	if err != nil {
		return app.Cluster{}, err
	}
	return app.Cluster{
		AppName:     appName,
		Env:         env,
		Provider:    providerName,
		Credentials: creds,
		SSHKey:      sshKey,
		Output:      resolveOutput(cmd),
	}, nil
}

// readSecretsFromCluster reads secret values from the cluster for managed compilation.
func readSecretsFromCluster(cmd *cobra.Command, cluster app.Cluster, keys []string) (map[string]string, error) {
	env := make(map[string]string, len(keys))
	for _, key := range keys {
		val, err := app.SecretReveal(cmd.Context(), app.SecretRevealRequest{
			Cluster: cluster,
			Key:     key,
		})
		if err != nil {
			return nil, fmt.Errorf("secret %q: %w", key, err)
		}
		env[key] = val
	}
	return env, nil
}

// deleteByShape deletes all resources owned by a managed bundle using its shape.
func deleteByShape(cmd *cobra.Command, cluster app.Cluster, shape managed.BundleShape) error {
	for _, name := range shape.Crons {
		err := app.CronDelete(cmd.Context(), app.CronDeleteRequest{Cluster: cluster, Name: name})
		if rerr := render.HandleDeleteResult(err, cluster.Output); rerr != nil {
			return rerr
		}
	}
	for _, name := range shape.Services {
		err := app.ServiceDelete(cmd.Context(), app.ServiceDeleteRequest{Cluster: cluster, Name: name})
		if rerr := render.HandleDeleteResult(err, cluster.Output); rerr != nil {
			return rerr
		}
	}
	for _, name := range shape.Storages {
		err := app.StorageDelete(cmd.Context(), app.StorageDeleteRequest{Cluster: cluster, Name: name})
		if rerr := render.HandleDeleteResult(err, cluster.Output); rerr != nil {
			return rerr
		}
	}
	for _, key := range shape.SecretKeys {
		err := app.SecretDelete(cmd.Context(), app.SecretDeleteRequest{Cluster: cluster, Key: key})
		if rerr := render.HandleDeleteResult(err, cluster.Output); rerr != nil {
			return rerr
		}
	}
	for _, name := range shape.Volumes {
		err := app.VolumeDelete(cmd.Context(), app.VolumeDeleteRequest{Cluster: cluster, Name: name})
		if rerr := render.HandleDeleteResult(err, cluster.Output); rerr != nil {
			return rerr
		}
	}
	return nil
}

// verifyManagedKind checks that a service exists in the cluster as a managed
// service of the expected kind. Returns error if not found or wrong category.
func verifyManagedKind(cmd *cobra.Command, cluster app.Cluster, name, expectedKind string) error {
	services, err := app.ManagedList(cmd.Context(), app.ManagedListRequest{
		Cluster: cluster,
		Kind:    "", // list all managed services
	})
	if err != nil {
		return fmt.Errorf("verify managed service: %w", err)
	}
	for _, svc := range services {
		if svc.Name == name {
			if svc.ManagedKind != expectedKind {
				return fmt.Errorf("service %q is managed kind %q, not %q", name, svc.ManagedKind, expectedKind)
			}
			return nil
		}
	}
	return fmt.Errorf("service %q not found or not a managed %s", name, expectedKind)
}

// verifyStorageExists checks that storage credentials exist in the cluster.
func verifyStorageExists(cmd *cobra.Command, cluster app.Cluster, storageName string) error {
	items, err := app.StorageList(cmd.Context(), app.StorageListRequest{Cluster: cluster})
	if err != nil {
		return fmt.Errorf("verify storage: %w", err)
	}
	for _, item := range items {
		if item.Name == storageName {
			return nil
		}
	}
	return fmt.Errorf("storage %q not found — run 'nvoi storage set %s' first", storageName, storageName)
}

// execOperation dispatches a single managed bundle operation to the
// corresponding pkg/core function.
func execOperation(ctx context.Context, cluster app.Cluster, op managed.Operation) error {
	p := op.Params
	switch op.Kind {
	case "secret.set":
		return app.SecretSet(ctx, app.SecretSetRequest{
			Cluster: cluster,
			Key:     op.Name,
			Value:   getString(p, "value"),
		})
	case "volume.set":
		_, err := app.VolumeSet(ctx, app.VolumeSetRequest{
			Cluster: cluster,
			Name:    op.Name,
			Size:    getInt(p, "size"),
			Server:  getString(p, "server"),
		})
		return err
	case "storage.set":
		return app.StorageSet(ctx, app.StorageSetRequest{
			Cluster:    cluster,
			Name:       op.Name,
			CORS:       getBool(p, "cors"),
			ExpireDays: getInt(p, "expire_days"),
		})
	case "service.set":
		return app.ServiceSet(ctx, app.ServiceSetRequest{
			Cluster:     cluster,
			Name:        op.Name,
			Image:       getString(p, "image"),
			Port:        getInt(p, "port"),
			Command:     getString(p, "command"),
			EnvVars:     getStringSlice(p, "env"),
			Secrets:     getStringSlice(p, "secrets"),
			Volumes:     getStringSlice(p, "volumes"),
			ManagedKind: getString(p, "managed_kind"),
		})
	case "cron.set":
		return app.CronSet(ctx, app.CronSetRequest{
			Cluster:  cluster,
			Name:     op.Name,
			Image:    getString(p, "image"),
			Command:  getString(p, "command"),
			EnvVars:  getStringSlice(p, "env"),
			Secrets:  getStringSlice(p, "secrets"),
			Storages: getStringSlice(p, "storage"),
			Schedule: getString(p, "schedule"),
			Server:   getString(p, "server"),
		})
	default:
		return fmt.Errorf("managed: unknown operation kind %q", op.Kind)
	}
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func getInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

func getBool(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func getStringSlice(m map[string]any, key string) []string {
	switch v := m[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
