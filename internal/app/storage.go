package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/kube"
	"github.com/getnvoi/nvoi/internal/provider"
)

type StorageSetRequest struct {
	AppName         string
	Env             string
	ComputeProvider string
	ComputeCreds    map[string]string
	StorageProvider string
	StorageCreds    map[string]string
	SSHKey          []byte
	Name            string // logical name (e.g. "assets")
	Bucket          string // explicit bucket name override (empty = derived from naming convention)
	CORS            bool
	ExpireDays      int
}

func StorageSet(ctx context.Context, req StorageSetRequest) error {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return err
	}

	bucket, err := provider.ResolveBucket(req.StorageProvider, req.StorageCreds)
	if err != nil {
		return err
	}

	// Derive bucket name from naming convention if not explicitly set
	bucketName := req.Bucket
	if bucketName == "" {
		bucketName = names.Bucket(req.Name)
	}

	fmt.Printf("==> storage set %s\n", req.Name)

	// 1. Validate credentials
	if err := bucket.ValidateCredentials(ctx); err != nil {
		return err
	}

	// 2. Create bucket (idempotent)
	fmt.Printf("  ensuring bucket %s...\n", bucketName)
	if err := bucket.EnsureBucket(ctx, bucketName); err != nil {
		return err
	}
	fmt.Printf("  ✓ bucket %s\n", bucketName)

	// 3. CORS
	if req.CORS {
		fmt.Printf("  setting CORS...\n")
		if err := bucket.SetCORS(ctx, bucketName, []string{"*"}, nil); err != nil {
			return fmt.Errorf("set cors: %w", err)
		}
		fmt.Printf("  ✓ CORS enabled\n")
	}

	// 4. Lifecycle
	if req.ExpireDays > 0 {
		fmt.Printf("  setting lifecycle (expire: %d days)...\n", req.ExpireDays)
		if err := bucket.SetLifecycle(ctx, bucketName, req.ExpireDays); err != nil {
			return fmt.Errorf("set lifecycle: %w", err)
		}
		fmt.Printf("  ✓ lifecycle set\n")
	}

	// 5. Store credentials as k8s secrets
	creds := bucket.Credentials()
	prov, err := provider.ResolveCompute(req.ComputeProvider, req.ComputeCreds)
	if err != nil {
		return err
	}
	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return err
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
	if err != nil {
		return fmt.Errorf("ssh master: %w", err)
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	secretName := names.KubeSecrets()
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
	AppName         string
	Env             string
	ComputeProvider string
	ComputeCreds    map[string]string
	StorageProvider string
	StorageCreds    map[string]string
	SSHKey          []byte
	Name            string
}

// StorageDelete removes the bucket from the provider and the secrets from the cluster.
// Bucket must be empty first (use StorageEmpty). Provider returns hard error if not.
func StorageDelete(ctx context.Context, req StorageDeleteRequest) error {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return err
	}

	// Delete bucket from provider
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

	// Remove secrets from cluster
	prov, err := provider.ResolveCompute(req.ComputeProvider, req.ComputeCreds)
	if err != nil {
		return err
	}
	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return err
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
	if err != nil {
		return fmt.Errorf("ssh master: %w", err)
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	secretName := names.KubeSecrets()
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

// StorageEmpty deletes all objects in a bucket. Required before bucket deletion.
func StorageEmpty(ctx context.Context, req StorageEmptyRequest) error {
	names, err := core.NewNames(req.AppName, req.Env)
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

type StorageEmptyRequest struct {
	AppName         string
	Env             string
	StorageProvider string
	StorageCreds    map[string]string
	Name            string
}

type StorageListRequest struct {
	AppName         string
	Env             string
	ComputeProvider string
	ComputeCreds    map[string]string
	SSHKey          []byte
}

type StorageItem struct {
	Name   string // logical name (e.g. "assets")
	Bucket string // cloud bucket name
}

// StorageList discovers storage entries by scanning k8s secrets for STORAGE_*_BUCKET keys.
func StorageList(ctx context.Context, req StorageListRequest) ([]StorageItem, error) {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return nil, err
	}
	prov, err := provider.ResolveCompute(req.ComputeProvider, req.ComputeCreds)
	if err != nil {
		return nil, err
	}
	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return nil, err
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("ssh master: %w", err)
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	secretName := names.KubeSecrets()
	keys, err := kube.ListSecretKeys(ctx, ssh, ns, secretName)
	if err != nil {
		return nil, nil // no secrets = no storage
	}

	// Scan for STORAGE_*_BUCKET keys and extract names
	var items []StorageItem
	for _, key := range keys {
		storageName, ok := parseStorageBucketKey(key)
		if !ok {
			continue
		}
		// Read the bucket value
		bucket, err := kube.GetSecretValue(ctx, ssh, ns, secretName, key)
		if err != nil {
			continue
		}
		items = append(items, StorageItem{Name: storageName, Bucket: bucket})
	}
	return items, nil
}

// parseStorageBucketKey extracts the storage name from a STORAGE_*_BUCKET key.
// "STORAGE_ASSETS_BUCKET" → ("assets", true)
func parseStorageBucketKey(key string) (string, bool) {
	if !strings.HasPrefix(key, "STORAGE_") || !strings.HasSuffix(key, "_BUCKET") {
		return "", false
	}
	// Strip prefix "STORAGE_" and suffix "_BUCKET"
	name := key[len("STORAGE_") : len(key)-len("_BUCKET")]
	if name == "" {
		return "", false
	}
	return strings.ToLower(strings.ReplaceAll(name, "_", "-")), true
}

// StorageSecretKeys returns the 4 conventional secret keys for a storage name.
func StorageSecretKeys(name string) []string {
	prefix := core.StorageEnvPrefix(name)
	return []string{
		prefix + "_ENDPOINT",
		prefix + "_BUCKET",
		prefix + "_ACCESS_KEY_ID",
		prefix + "_SECRET_ACCESS_KEY",
	}
}
