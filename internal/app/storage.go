package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/kube"
	"github.com/getnvoi/nvoi/internal/provider"
)

type StorageSetRequest struct {
	Cluster
	StorageProvider string
	StorageCreds    map[string]string
	Name            string // logical name (e.g. "assets")
	Bucket          string // explicit bucket name override (empty = derived)
	CORS            bool
	ExpireDays      int
}

func StorageSet(ctx context.Context, req StorageSetRequest) error {
	names, err := req.Names()
	if err != nil {
		return err
	}

	bucket, err := provider.ResolveBucket(req.StorageProvider, req.StorageCreds)
	if err != nil {
		return err
	}

	bucketName := req.Bucket
	if bucketName == "" {
		bucketName = names.Bucket(req.Name)
	}

	fmt.Printf("==> storage set %s\n", req.Name)

	if err := bucket.ValidateCredentials(ctx); err != nil {
		return err
	}

	fmt.Printf("  ensuring bucket %s...\n", bucketName)
	if err := bucket.EnsureBucket(ctx, bucketName); err != nil {
		return err
	}
	fmt.Printf("  ✓ bucket %s\n", bucketName)

	if req.CORS {
		fmt.Printf("  setting CORS...\n")
		if err := bucket.SetCORS(ctx, bucketName, []string{"*"}, nil); err != nil {
			return fmt.Errorf("set cors: %w", err)
		}
		fmt.Printf("  ✓ CORS enabled\n")
	}

	if req.ExpireDays > 0 {
		fmt.Printf("  setting lifecycle (expire: %d days)...\n", req.ExpireDays)
		if err := bucket.SetLifecycle(ctx, bucketName, req.ExpireDays); err != nil {
			return fmt.Errorf("set lifecycle: %w", err)
		}
		fmt.Printf("  ✓ lifecycle set\n")
	}

	// Store credentials as k8s secrets
	creds := bucket.Credentials()
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
	prefix := core.StorageEnvPrefix(req.Name)
	secrets := map[string]string{
		prefix + "_ENDPOINT":          creds.Endpoint,
		prefix + "_BUCKET":            bucketName,
		prefix + "_ACCESS_KEY_ID":     creds.AccessKeyID,
		prefix + "_SECRET_ACCESS_KEY": creds.SecretAccessKey,
	}

	fmt.Printf("  storing secrets...\n")
	for key, value := range secrets {
		if err := kube.UpsertSecretKey(ctx, ssh, ns, secretName, key, value); err != nil {
			return fmt.Errorf("store %s: %w", key, err)
		}
	}

	fmt.Printf("  ✓ secrets stored:\n")
	for key := range secrets {
		fmt.Printf("    %s\n", key)
	}
	fmt.Printf("\n  Use with service set:\n    --storage %s\n", req.Name)

	return nil
}

type StorageDeleteRequest struct {
	Cluster
	StorageProvider string
	StorageCreds    map[string]string
	Name            string
}

func StorageDelete(ctx context.Context, req StorageDeleteRequest) error {
	names, err := req.Names()
	if err != nil {
		return err
	}

	if req.StorageProvider != "" {
		bucket, err := provider.ResolveBucket(req.StorageProvider, req.StorageCreds)
		if err != nil {
			return err
		}
		bucketName := names.Bucket(req.Name)
		fmt.Printf("==> storage delete %s\n", req.Name)
		fmt.Printf("  deleting bucket %s...\n", bucketName)
		if err := bucket.DeleteBucket(ctx, bucketName); err != nil {
			return fmt.Errorf("delete bucket: %w", err)
		}
		fmt.Printf("  ✓ bucket deleted\n")
	} else {
		fmt.Printf("==> storage delete %s\n", req.Name)
	}

	ssh, names2, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names2.KubeNamespace()
	secretName := names2.KubeSecrets()
	prefix := core.StorageEnvPrefix(req.Name)
	keys := []string{
		prefix + "_ENDPOINT",
		prefix + "_BUCKET",
		prefix + "_ACCESS_KEY_ID",
		prefix + "_SECRET_ACCESS_KEY",
	}

	for _, key := range keys {
		_ = kube.DeleteSecretKey(ctx, ssh, ns, secretName, key)
	}
	fmt.Printf("  ✓ secrets removed\n")
	return nil
}

type StorageEmptyRequest struct {
	Cluster
	StorageProvider string
	StorageCreds    map[string]string
	Name            string
}

func StorageEmpty(ctx context.Context, req StorageEmptyRequest) error {
	names, err := req.Names()
	if err != nil {
		return err
	}

	bucket, err := provider.ResolveBucket(req.StorageProvider, req.StorageCreds)
	if err != nil {
		return err
	}

	bucketName := names.Bucket(req.Name)
	fmt.Printf("==> storage empty %s\n", req.Name)
	fmt.Printf("  emptying bucket %s...\n", bucketName)
	if err := bucket.EmptyBucket(ctx, bucketName); err != nil {
		return fmt.Errorf("empty bucket: %w", err)
	}
	fmt.Printf("  ✓ bucket emptied\n")
	return nil
}

type StorageListRequest struct {
	Cluster
}

type StorageItem struct {
	Name   string // logical name (e.g. "assets")
	Bucket string // cloud bucket name
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
		return nil, nil
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
	prefix := core.StorageEnvPrefix(name)
	return []string{
		prefix + "_ENDPOINT",
		prefix + "_BUCKET",
		prefix + "_ACCESS_KEY_ID",
		prefix + "_SECRET_ACCESS_KEY",
	}
}
