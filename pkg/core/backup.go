// Package core contains the business logic for all nvoi operations — compute, service, DNS, ingress, storage, secrets, builds, and managed services.
package core

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

// ── Backup create ─────────────────────────────────────────────────────────────

type BackupCreateRequest struct {
	Cluster
	CronName string // e.g. "db-backup"
}

func BackupCreate(ctx context.Context, req BackupCreateRequest) error {
	out := req.Log()
	out.Command("backup", "create", req.CronName)

	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	jobName := fmt.Sprintf("%s-manual-%d", req.CronName, time.Now().Unix())

	if err := kube.CreateJobFromCronJob(ctx, ssh, ns, req.CronName, jobName); err != nil {
		return err
	}
	out.Progress("waiting for " + jobName)

	if err := kube.WaitForJob(ctx, ssh, ns, jobName, out); err != nil {
		return err
	}
	out.Success("backup completed")
	return nil
}

// ── Backup list ───────────────────────────────────────────────────────────────

type BackupListRequest struct {
	Cluster
	Storage ProviderRef
	Name    string // storage name (e.g. "db-backups")
}

type BackupArtifact struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	LastModified string `json:"last_modified"`
	Service      string `json:"service"`
	Bucket       string `json:"bucket"`
}

func BackupList(ctx context.Context, req BackupListRequest) ([]BackupArtifact, error) {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	secretName := names.KubeSecrets()

	creds, err := readStorageCreds(ctx, ssh, ns, secretName, req.Name)
	if err != nil {
		return nil, err
	}

	objects, err := s3.ListObjects(creds.endpoint, creds.accessKey, creds.secretKey, creds.bucket, "")
	if err != nil {
		return nil, fmt.Errorf("list backup objects: %w", err)
	}

	artifacts := make([]BackupArtifact, 0, len(objects))
	for _, obj := range objects {
		artifacts = append(artifacts, BackupArtifact{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			Service:      req.Name,
			Bucket:       creds.bucket,
		})
	}
	return artifacts, nil
}

// ── Backup download ───────────────────────────────────────────────────────────

type BackupDownloadRequest struct {
	Cluster
	Storage ProviderRef
	Name    string // storage name
	Key     string // object key to download
}

func BackupDownload(ctx context.Context, req BackupDownloadRequest, w io.Writer) error {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	secretName := names.KubeSecrets()

	creds, err := readStorageCreds(ctx, ssh, ns, secretName, req.Name)
	if err != nil {
		return err
	}

	reader, _, _, err := s3.GetStream(creds.endpoint, creds.accessKey, creds.secretKey, creds.bucket, req.Key)
	if err != nil {
		return fmt.Errorf("download backup %q: %w", req.Key, err)
	}
	defer reader.Close()

	_, err = io.Copy(w, reader)
	return err
}

// ── helpers ───────────────────────────────────────────────────────────────────

type storageCreds struct {
	endpoint  string
	bucket    string
	accessKey string
	secretKey string
}

func readStorageCreds(ctx context.Context, ssh utils.SSHClient, ns, secretName, storageName string) (storageCreds, error) {
	prefix := utils.StorageEnvPrefix(storageName)
	keys := map[string]*string{}
	var c storageCreds
	keys[prefix+"_ENDPOINT"] = &c.endpoint
	keys[prefix+"_BUCKET"] = &c.bucket
	keys[prefix+"_ACCESS_KEY_ID"] = &c.accessKey
	keys[prefix+"_SECRET_ACCESS_KEY"] = &c.secretKey

	for key, dest := range keys {
		if ctx.Err() != nil {
			return storageCreds{}, ctx.Err()
		}
		val, err := kube.GetSecretValue(ctx, ssh, ns, secretName, key)
		if err != nil {
			return storageCreds{}, fmt.Errorf("storage credential %q: %w", key, err)
		}
		*dest = val
	}
	return c, nil
}
