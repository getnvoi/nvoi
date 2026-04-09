package core

import (
	"context"
	"fmt"
	"os"

	"github.com/getnvoi/nvoi/internal/commands"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/managed"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func (d *DirectBackend) DatabaseSet(ctx context.Context, name string, opts commands.ManagedOpts) error {
	env, err := d.readSecretsFromCluster(ctx, opts.Secrets)
	if err != nil {
		return err
	}

	if opts.BackupStorage != "" {
		if err := d.verifyStorageExists(ctx, opts.BackupStorage); err != nil {
			return err
		}
	}

	params := map[string]any{
		"image":          opts.Image,
		"volume_size":    opts.VolumeSize,
		"backup_storage": opts.BackupStorage,
		"backup_cron":    opts.BackupCron,
	}

	if opts.BackupStorage != "" && opts.BackupCron != "" {
		backupImage, err := d.ensureBackupImage(ctx, opts.Image)
		if err != nil {
			return err
		}
		params["backup_image"] = backupImage
	}

	result, err := managed.Compile(managed.Request{
		Kind:    opts.Kind,
		Name:    name,
		Env:     env,
		Params:  params,
		Context: managed.Context{DefaultVolumeServer: "master"},
	})
	if err != nil {
		return err
	}
	return d.execBundle(ctx, result.Bundle)
}

func (d *DirectBackend) DatabaseDelete(ctx context.Context, name, kind string) error {
	return d.deleteByShape(ctx, kind, name)
}

func (d *DirectBackend) DatabaseList(ctx context.Context) error {
	return d.listManaged(ctx, "database")
}

func (d *DirectBackend) BackupCreate(ctx context.Context, name, kind string) error {
	if err := d.verifyManagedKind(ctx, name, kind); err != nil {
		return err
	}
	return app.BackupCreate(ctx, app.BackupCreateRequest{
		Cluster:  d.cluster,
		CronName: name + "-backup",
	})
}

func (d *DirectBackend) BackupList(ctx context.Context, name, kind, backupStorage string) error {
	if err := d.verifyManagedKind(ctx, name, kind); err != nil {
		return err
	}
	if backupStorage == "" {
		backupStorage = utils.BackupStorageName(name)
	}
	if err := d.verifyStorageExists(ctx, backupStorage); err != nil {
		return err
	}
	artifacts, err := app.BackupList(ctx, app.BackupListRequest{
		Cluster: d.cluster,
		Name:    backupStorage,
	})
	if err != nil {
		return err
	}
	if len(artifacts) == 0 {
		d.cluster.Output.Info("no backups found")
		return nil
	}
	for _, a := range artifacts {
		d.cluster.Output.Success(fmt.Sprintf("%s  %d bytes  %s", a.Key, a.Size, a.LastModified))
	}
	return nil
}

func (d *DirectBackend) BackupDownload(ctx context.Context, name, kind, backupStorage, key string) error {
	if err := d.verifyManagedKind(ctx, name, kind); err != nil {
		return err
	}
	if backupStorage == "" {
		backupStorage = utils.BackupStorageName(name)
	}
	if err := d.verifyStorageExists(ctx, backupStorage); err != nil {
		return err
	}
	return app.BackupDownload(ctx, app.BackupDownloadRequest{
		Cluster: d.cluster,
		Name:    backupStorage,
		Key:     key,
	}, os.Stdout)
}
