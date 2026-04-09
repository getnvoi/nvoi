package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/managed"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// readSecretsFromCluster reads secret values from the cluster for managed compilation.
func (d *DirectBackend) readSecretsFromCluster(ctx context.Context, keys []string) (map[string]string, error) {
	env := make(map[string]string, len(keys))
	for _, key := range keys {
		val, err := app.SecretReveal(ctx, app.SecretRevealRequest{Cluster: d.cluster, Key: key})
		if err != nil {
			return nil, fmt.Errorf("secret %q: %w", key, err)
		}
		env[key] = val
	}
	return env, nil
}

// execBundle dispatches all operations in a managed bundle.
func (d *DirectBackend) execBundle(ctx context.Context, bundle managed.Bundle) error {
	for _, op := range bundle.Operations {
		if err := execOperation(ctx, d.cluster, op); err != nil {
			return err
		}
	}
	return nil
}

// deleteByShape deletes all resources owned by a managed bundle.
func (d *DirectBackend) deleteByShape(ctx context.Context, kind, name string) error {
	shape, err := managed.Shape(kind, name)
	if err != nil {
		return err
	}
	type deleteFunc func() error
	var ops []deleteFunc
	for _, n := range shape.Crons {
		n := n
		ops = append(ops, func() error {
			return app.CronDelete(ctx, app.CronDeleteRequest{Cluster: d.cluster, Name: n})
		})
	}
	for _, n := range shape.Services {
		n := n
		ops = append(ops, func() error {
			return app.ServiceDelete(ctx, app.ServiceDeleteRequest{Cluster: d.cluster, Name: n})
		})
	}
	for _, n := range shape.Storages {
		n := n
		ops = append(ops, func() error {
			return app.StorageDelete(ctx, app.StorageDeleteRequest{Cluster: d.cluster, Name: n})
		})
	}
	for _, key := range shape.SecretKeys {
		key := key
		ops = append(ops, func() error {
			return app.SecretDelete(ctx, app.SecretDeleteRequest{Cluster: d.cluster, Key: key})
		})
	}
	for _, n := range shape.Volumes {
		n := n
		ops = append(ops, func() error {
			return app.VolumeDelete(ctx, app.VolumeDeleteRequest{Cluster: d.cluster, Name: n})
		})
	}
	for _, op := range ops {
		if err := render.HandleDeleteResult(op(), d.cluster.Output); err != nil {
			return err
		}
	}
	return nil
}

// listManaged lists managed services for a given category.
func (d *DirectBackend) listManaged(ctx context.Context, category string) error {
	kinds := managed.KindsForCategory(category)
	var all []app.ManagedService
	for _, kind := range kinds {
		services, err := app.ManagedList(ctx, app.ManagedListRequest{Cluster: d.cluster, Kind: kind})
		if err != nil {
			return err
		}
		all = append(all, services...)
	}
	if len(all) == 0 {
		d.cluster.Output.Info(fmt.Sprintf("no managed %ss found", category))
		return nil
	}
	for _, svc := range all {
		children := strings.Join(svc.Children, ", ")
		d.cluster.Output.Success(fmt.Sprintf("%s  type=%s  %s  %s  children=[%s]", svc.Name, svc.ManagedKind, svc.Image, svc.Ready, children))
	}
	return nil
}

// verifyManagedKind checks that a service exists as a managed service of the expected kind.
func (d *DirectBackend) verifyManagedKind(ctx context.Context, name, expectedKind string) error {
	services, err := app.ManagedList(ctx, app.ManagedListRequest{Cluster: d.cluster, Kind: ""})
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
func (d *DirectBackend) verifyStorageExists(ctx context.Context, storageName string) error {
	items, err := app.StorageList(ctx, app.StorageListRequest{Cluster: d.cluster})
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

// ensureBackupImage builds and pushes the postgres backup image if it doesn't exist.
func (d *DirectBackend) ensureBackupImage(ctx context.Context, baseImage string) (string, error) {
	if baseImage == "" {
		baseImage = "postgres:17"
	}
	version := "latest"
	if parts := strings.SplitN(baseImage, ":", 2); len(parts) == 2 {
		version = parts[1]
	}

	imageName := "nvoi-pg-backup"
	d.cluster.Output.Progress(fmt.Sprintf("ensuring backup image %s:%s", imageName, version))

	master, _, _, err := d.cluster.Master(ctx)
	if err != nil {
		return "", err
	}
	registryAddr := master.PrivateIP + ":5000"
	fullRef := registryAddr + "/" + imageName + ":" + version

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", utils.DefaultUser, d.cluster.SSHKey)
	if err != nil {
		return "", err
	}
	defer ssh.Close()

	checkCmd := fmt.Sprintf("curl -sf http://%s/v2/%s/tags/list 2>/dev/null | grep -q %q", registryAddr, imageName, version)
	if _, err := ssh.Run(ctx, checkCmd); err == nil {
		d.cluster.Output.Success(fmt.Sprintf("backup image %s exists", fullRef))
		return fullRef, nil
	}

	dockerfile := fmt.Sprintf("FROM %s\nRUN apt-get update && apt-get install -y awscli && rm -rf /var/lib/apt/lists/*\n", baseImage)
	dfPath := "/tmp/nvoi-pg-backup.Dockerfile"
	if err := ssh.Upload(ctx, strings.NewReader(dockerfile), dfPath, 0644); err != nil {
		return "", fmt.Errorf("upload Dockerfile: %w", err)
	}

	d.cluster.Output.Progress(fmt.Sprintf("building backup image %s", fullRef))
	buildCmd := fmt.Sprintf("docker buildx build --tag %s --output type=image,push=true,registry.insecure=true -f %s /tmp", fullRef, dfPath)
	if _, err := ssh.Run(ctx, buildCmd); err != nil {
		return "", fmt.Errorf("build backup image: %w", err)
	}

	d.cluster.Output.Success(fmt.Sprintf("backup image %s ready", fullRef))
	return fullRef, nil
}

// execOperation dispatches a single managed bundle operation.
func execOperation(ctx context.Context, cluster app.Cluster, op managed.Operation) error {
	p := op.Params
	switch op.Kind {
	case "secret.set":
		return app.SecretSet(ctx, app.SecretSetRequest{
			Cluster: cluster, Key: op.Name, Value: utils.GetString(p, "value"),
		})
	case "volume.set":
		_, err := app.VolumeSet(ctx, app.VolumeSetRequest{
			Cluster: cluster, Name: op.Name,
			Size: utils.GetInt(p, "size"), Server: utils.GetString(p, "server"),
		})
		return err
	case "storage.set":
		return app.StorageSet(ctx, app.StorageSetRequest{
			Cluster: cluster, Name: op.Name,
			CORS: utils.GetBool(p, "cors"), ExpireDays: utils.GetInt(p, "expire_days"),
		})
	case "service.set":
		return app.ServiceSet(ctx, app.ServiceSetRequest{
			Cluster: cluster, Name: op.Name,
			Image: utils.GetString(p, "image"), Port: utils.GetInt(p, "port"),
			Command: utils.GetString(p, "command"), EnvVars: utils.GetStringSlice(p, "env"),
			Secrets: utils.GetStringSlice(p, "secrets"), Volumes: utils.GetStringSlice(p, "volumes"),
			ManagedKind: utils.GetString(p, "managed_kind"),
		})
	case "cron.set":
		return app.CronSet(ctx, app.CronSetRequest{
			Cluster: cluster, Name: op.Name,
			Image: utils.GetString(p, "image"), Command: utils.GetString(p, "command"),
			EnvVars: utils.GetStringSlice(p, "env"), Secrets: utils.GetStringSlice(p, "secrets"),
			Storages: utils.GetStringSlice(p, "storage"), Schedule: utils.GetString(p, "schedule"),
			Server: utils.GetString(p, "server"),
		})
	default:
		return fmt.Errorf("managed: unknown operation kind %q", op.Kind)
	}
}
