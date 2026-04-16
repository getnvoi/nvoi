package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type StorageSetRequest struct {
	Cluster
	Output     Output
	Storage    ProviderRef
	Name       string
	Bucket     string
	CORS       bool
	ExpireDays int
}

func StorageSet(ctx context.Context, req StorageSetRequest) (map[string]string, error) {
	out := log(req.Output)
	names, err := req.Names()
	if err != nil {
		return nil, err
	}

	bucket, err := provider.ResolveBucket(req.Storage.Name, req.Storage.Creds)
	if err != nil {
		return nil, err
	}

	bucketName := req.Bucket
	if bucketName == "" {
		bucketName = names.Bucket(req.Name)
	}

	out.Command("storage", "set", req.Name)

	if err := bucket.ValidateCredentials(ctx); err != nil {
		return nil, err
	}

	out.Progress(fmt.Sprintf("ensuring bucket %s", bucketName))
	if err := bucket.EnsureBucket(ctx, bucketName); err != nil {
		return nil, err
	}
	out.Success(fmt.Sprintf("bucket %s", bucketName))

	if req.CORS {
		out.Progress("setting CORS")
		if err := bucket.SetCORS(ctx, bucketName, []string{"*"}, nil); err != nil {
			return nil, fmt.Errorf("set cors: %w", err)
		}
		out.Success("CORS enabled")
	}

	if req.ExpireDays > 0 {
		out.Progress(fmt.Sprintf("setting lifecycle (expire: %d days)", req.ExpireDays))
		if err := bucket.SetLifecycle(ctx, bucketName, req.ExpireDays); err != nil {
			return nil, fmt.Errorf("set lifecycle: %w", err)
		}
		out.Success("lifecycle set")
	}

	creds, err := bucket.Credentials(ctx)
	if err != nil {
		return nil, err
	}

	prefix := utils.StorageEnvPrefix(req.Name)
	result := map[string]string{
		prefix + "_ENDPOINT":          creds.Endpoint,
		prefix + "_BUCKET":            bucketName,
		prefix + "_ACCESS_KEY_ID":     creds.AccessKeyID,
		prefix + "_SECRET_ACCESS_KEY": creds.SecretAccessKey,
	}

	out.Success("credentials resolved")
	return result, nil
}

type StorageDeleteRequest struct {
	Cluster
	Output  Output
	Storage ProviderRef
	Name    string
}

func StorageDelete(ctx context.Context, req StorageDeleteRequest) error {
	out := log(req.Output)
	names, err := req.Names()
	if err != nil {
		return err
	}

	out.Command("storage", "delete", req.Name)

	if req.Storage.Name == "" {
		return nil
	}

	bucket, err := provider.ResolveBucket(req.Storage.Name, req.Storage.Creds)
	if err != nil {
		return err
	}
	bucketName := names.Bucket(req.Name)
	if err := bucket.DeleteBucket(ctx, bucketName); err != nil {
		if errors.Is(err, utils.ErrNotFound) {
			out.Success(fmt.Sprintf("%s already deleted", bucketName))
			return utils.ErrNotFound
		}
		return err
	}
	out.Success(fmt.Sprintf("%s deleted", bucketName))
	return nil
}

type StorageEmptyRequest struct {
	Cluster
	Output  Output
	Storage ProviderRef
	Name    string
}

func StorageEmpty(ctx context.Context, req StorageEmptyRequest) error {
	out := log(req.Output)
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
	Output       Output
	StorageNames []string // from cfg — config is the source of truth
}

type StorageItem struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
}

func StorageList(_ context.Context, req StorageListRequest) ([]StorageItem, error) {
	names, err := req.Cluster.Names()
	if err != nil {
		return nil, err
	}

	items := make([]StorageItem, 0, len(req.StorageNames))
	for _, name := range req.StorageNames {
		items = append(items, StorageItem{Name: name, Bucket: names.Bucket(name)})
	}
	return items, nil
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
