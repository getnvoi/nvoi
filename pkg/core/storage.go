package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

type StorageSetRequest struct {
	Cluster
	Storage    ProviderRef
	Name       string
	Bucket     string
	CORS       bool
	ExpireDays int
}

func StorageSet(ctx context.Context, req StorageSetRequest) error {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return err
	}

	bucket, err := provider.ResolveBucket(req.Storage.Name, req.Storage.Creds)
	if err != nil {
		return err
	}

	bucketName := req.Bucket
	if bucketName == "" {
		bucketName = names.Bucket(req.Name)
	}

	out.Command("storage", "set", req.Name)

	if err := bucket.ValidateCredentials(ctx); err != nil {
		return err
	}

	out.Progress(fmt.Sprintf("ensuring bucket %s", bucketName))
	if err := bucket.EnsureBucket(ctx, bucketName); err != nil {
		return err
	}
	out.Success(fmt.Sprintf("bucket %s", bucketName))

	if req.CORS {
		out.Progress("setting CORS")
		if err := bucket.SetCORS(ctx, bucketName, []string{"*"}, nil); err != nil {
			return fmt.Errorf("set cors: %w", err)
		}
		out.Success("CORS enabled")
	}

	if req.ExpireDays > 0 {
		out.Progress(fmt.Sprintf("setting lifecycle (expire: %d days)", req.ExpireDays))
		if err := bucket.SetLifecycle(ctx, bucketName, req.ExpireDays); err != nil {
			return fmt.Errorf("set lifecycle: %w", err)
		}
		out.Success("lifecycle set")
	}

	creds, err := bucket.Credentials(ctx)
	if err != nil {
		return err
	}
	ssh, names2, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names2.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	secretName := names2.KubeSecrets()
	prefix := utils.StorageEnvPrefix(req.Name)
	secrets := map[string]string{
		prefix + "_ENDPOINT":          creds.Endpoint,
		prefix + "_BUCKET":            bucketName,
		prefix + "_ACCESS_KEY_ID":     creds.AccessKeyID,
		prefix + "_SECRET_ACCESS_KEY": creds.SecretAccessKey,
	}

	out.Progress("storing secrets")
	for key, value := range secrets {
		if err := kube.UpsertSecretKey(ctx, ssh, ns, secretName, key, value); err != nil {
			return fmt.Errorf("store %s: %w", key, err)
		}
	}

	out.Success("secrets stored")
	for key := range secrets {
		out.Info(key)
	}
	out.Info(fmt.Sprintf("use with service set: --storage %s", req.Name))

	return nil
}

type StorageDeleteRequest struct {
	Cluster
	Storage ProviderRef
	Name    string
}

func StorageDelete(ctx context.Context, req StorageDeleteRequest) error {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return err
	}

	out.Command("storage", "delete", req.Name)

	if req.Storage.Name != "" {
		bucket, err := provider.ResolveBucket(req.Storage.Name, req.Storage.Creds)
		if err != nil {
			return err
		}
		bucketName := names.Bucket(req.Name)
		out.Progress(fmt.Sprintf("deleting bucket %s", bucketName))
		if err := bucket.DeleteBucket(ctx, bucketName); err != nil {
			return err // ErrNotFound or real error — caller handles both
		}
	}

	ssh, names2, err := req.Cluster.SSH(ctx)
	if errors.Is(err, ErrNoMaster) {
		return ErrNoMaster
	}
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names2.KubeNamespace()
	secretName := names2.KubeSecrets()
	prefix := utils.StorageEnvPrefix(req.Name)
	keys := []string{
		prefix + "_ENDPOINT",
		prefix + "_BUCKET",
		prefix + "_ACCESS_KEY_ID",
		prefix + "_SECRET_ACCESS_KEY",
	}

	for _, key := range keys {
		_ = kube.DeleteSecretKey(ctx, ssh, ns, secretName, key)
	}
	return nil
}

type StorageEmptyRequest struct {
	Cluster
	Storage ProviderRef
	Name    string
}

func StorageEmpty(ctx context.Context, req StorageEmptyRequest) error {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return err
	}

	bucket, err := provider.ResolveBucket(req.Storage.Name, req.Storage.Creds)
	if err != nil {
		return err
	}

	bucketName := names.Bucket(req.Name)
	out.Command("storage", "empty", req.Name)
	out.Progress(fmt.Sprintf("emptying bucket %s", bucketName))
	return bucket.EmptyBucket(ctx, bucketName) // nil, ErrNotFound, or real error
}

type StorageListRequest struct {
	Cluster
}

type StorageItem struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
}

func StorageList(ctx context.Context, req StorageListRequest) ([]StorageItem, error) {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	secretName := names.KubeSecrets()
	keys, err := kube.ListSecretKeys(ctx, ssh, ns, secretName)
	if err != nil {
		return nil, err
	}

	var items []StorageItem
	for _, key := range keys {
		storageName, ok := parseStorageBucketKey(key)
		if !ok {
			continue
		}
		bucket, err := kube.GetSecretValue(ctx, ssh, ns, secretName, key)
		if err != nil {
			continue
		}
		items = append(items, StorageItem{Name: storageName, Bucket: bucket})
	}
	return items, nil
}

func parseStorageBucketKey(key string) (string, bool) {
	if !strings.HasPrefix(key, "STORAGE_") || !strings.HasSuffix(key, "_BUCKET") {
		return "", false
	}
	name := key[len("STORAGE_") : len(key)-len("_BUCKET")]
	if name == "" {
		return "", false
	}
	return strings.ToLower(strings.ReplaceAll(name, "_", "-")), true
}

func StorageSecretKeys(name string) []string {
	prefix := utils.StorageEnvPrefix(name)
	return []string{
		prefix + "_ENDPOINT",
		prefix + "_BUCKET",
		prefix + "_ACCESS_KEY_ID",
		prefix + "_SECRET_ACCESS_KEY",
	}
}
