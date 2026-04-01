package app

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/kube"
	"github.com/getnvoi/nvoi/internal/provider"
)

type SecretSetRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Key         string
	Value       string
}

func SecretSet(ctx context.Context, req SecretSetRequest) error {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
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
	fmt.Printf("==> secret set %s\n", req.Key)

	if err := kube.UpsertSecretKey(ctx, ssh, ns, secretName, req.Key, req.Value); err != nil {
		return err
	}
	fmt.Printf("  ✓ %s stored\n", req.Key)
	return nil
}

type SecretDeleteRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Key         string
}

func SecretDelete(ctx context.Context, req SecretDeleteRequest) error {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
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
	fmt.Printf("==> secret delete %s\n", req.Key)

	if err := kube.DeleteSecretKey(ctx, ssh, ns, secretName, req.Key); err != nil {
		return err
	}
	fmt.Printf("  ✓ %s removed\n", req.Key)
	return nil
}

type SecretListRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
}

func SecretList(ctx context.Context, req SecretListRequest) ([]string, error) {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return nil, err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
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
	return kube.ListSecretKeys(ctx, ssh, ns, secretName)
}

type SecretRevealRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Key         string
}

func SecretReveal(ctx context.Context, req SecretRevealRequest) (string, error) {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return "", err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return "", err
	}
	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return "", err
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
	if err != nil {
		return "", fmt.Errorf("ssh master: %w", err)
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	secretName := names.KubeSecrets()
	return kube.GetSecretValue(ctx, ssh, ns, secretName, req.Key)
}
