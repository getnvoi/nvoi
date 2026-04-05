package core

import (
	"context"
	"errors"

	"github.com/getnvoi/nvoi/pkg/kube"
)

type SecretSetRequest struct {
	Cluster
	Key   string
	Value string
}

func SecretSet(ctx context.Context, req SecretSetRequest) error {
	out := req.Log()
	out.Command("secret", "set", req.Key)

	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	if err := kube.UpsertSecretKey(ctx, ssh, ns, names.KubeSecrets(), req.Key, req.Value); err != nil {
		return err
	}
	out.Success(req.Key + " stored")
	return nil
}

type SecretDeleteRequest struct {
	Cluster
	Key string
}

func SecretDelete(ctx context.Context, req SecretDeleteRequest) error {
	out := req.Log()
	out.Command("secret", "delete", req.Key)

	ssh, names, err := req.Cluster.SSH(ctx)
	if errors.Is(err, ErrNoMaster) {
		return ErrNoMaster
	}
	if err != nil {
		return err
	}
	defer ssh.Close()

	return kube.DeleteSecretKey(ctx, ssh, names.KubeNamespace(), names.KubeSecrets(), req.Key)
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
