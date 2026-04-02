package app

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/kube"
)

type SecretSetRequest struct {
	Cluster
	Key   string
	Value string
}

func SecretSet(ctx context.Context, req SecretSetRequest) error {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	fmt.Printf("==> secret set %s\n", req.Key)
	if err := kube.UpsertSecretKey(ctx, ssh, ns, names.KubeSecrets(), req.Key, req.Value); err != nil {
		return err
	}
	fmt.Printf("  ✓ %s stored\n", req.Key)
	return nil
}

type SecretDeleteRequest struct {
	Cluster
	Key string
}

func SecretDelete(ctx context.Context, req SecretDeleteRequest) error {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	fmt.Printf("==> secret delete %s\n", req.Key)
	if err := kube.DeleteSecretKey(ctx, ssh, ns, names.KubeSecrets(), req.Key); err != nil {
		return err
	}
	fmt.Printf("  ✓ %s removed\n", req.Key)
	return nil
}

type SecretListRequest struct {
	Cluster
}

func SecretList(ctx context.Context, req SecretListRequest) ([]string, error) {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	return kube.ListSecretKeys(ctx, ssh, names.KubeNamespace(), names.KubeSecrets())
}

type SecretRevealRequest struct {
	Cluster
	Key string
}

func SecretReveal(ctx context.Context, req SecretRevealRequest) (string, error) {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return "", err
	}
	defer ssh.Close()

	return kube.GetSecretValue(ctx, ssh, names.KubeNamespace(), names.KubeSecrets(), req.Key)
}
