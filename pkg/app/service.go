package app

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
)

type ServiceSetRequest struct {
	Cluster
	Name       string
	Image      string
	Port       int
	Command    string
	Replicas   int
	EnvVars    []string // KEY=VALUE pairs
	Secrets    []string // secret key references (must exist in cluster)
	Storages   []string // storage names → expands to STORAGE_{NAME}_* secret refs
	Volumes    []string // name:/path
	HealthPath string
	Server     string
}

func ServiceSet(ctx context.Context, req ServiceSetRequest) error {
	out := req.Log()

	if req.Image == "" {
		return fmt.Errorf("--image is required")
	}

	master, names, prov, err := req.Cluster.Master(ctx)
	if err != nil {
		return err
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	// Resolve volumes — named volumes must exist as provider volumes
	managedVolPaths := map[string]string{}
	managed := false
	vols, _ := prov.ListVolumes(ctx, names.Labels())
	for _, mount := range req.Volumes {
		source, _, named, ok := core.ParseVolumeMount(mount)
		if !ok {
			return fmt.Errorf("invalid volume mount %q", mount)
		}
		if named {
			volName := names.Volume(source)
			found := false
			for _, v := range vols {
				if v.Name == volName {
					managedVolPaths[source] = names.VolumeMountPath(source)
					managed = true
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("volume %q not found — run 'volume set %s' first", source, source)
			}
		}
	}

	// Parse env vars
	var env []corev1.EnvVar
	for _, e := range req.EnvVars {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return fmt.Errorf("invalid env var %q — expected KEY=VALUE", e)
		}
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}

	// Expand --storage names into secret refs
	for _, storageName := range req.Storages {
		for _, key := range StorageSecretKeys(storageName) {
			req.Secrets = append(req.Secrets, key)
		}
	}

	// Validate secret references
	secretName := names.KubeSecrets()
	if len(req.Secrets) > 0 {
		existing, err := kube.ListSecretKeys(ctx, ssh, ns, secretName)
		if err != nil {
			return fmt.Errorf("cannot verify secrets — run 'secret set' first: %w", err)
		}
		for _, key := range req.Secrets {
			found := false
			for _, k := range existing {
				if k == key {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("secret %q not found — run 'nvoi secret set %s <value>' first", key, key)
			}
		}
	}

	spec := kube.ServiceSpec{
		Name:       req.Name,
		Image:      req.Image,
		Port:       req.Port,
		Command:    req.Command,
		Replicas:   req.Replicas,
		Env:        env,
		Secrets:    req.Secrets,
		SecretName: secretName,
		Volumes:    req.Volumes,
		HealthPath: req.HealthPath,
		Server:     req.Server,
		Managed:    managed,
	}

	out.Command("service", "set", req.Name)

	yaml, workloadKind, err := kube.GenerateYAML(spec, names, managedVolPaths)
	if err != nil {
		return fmt.Errorf("generate manifest: %w", err)
	}

	if err := kube.Apply(ctx, ssh, ns, yaml); err != nil {
		return err
	}
	out.Success("applied")

	out.Progress("waiting for rollout")
	if err := kube.WaitRollout(ctx, ssh, ns, req.Name, workloadKind, out); err != nil {
		return err
	}
	out.Success(req.Name + " ready")

	return nil
}

type ServiceDeleteRequest struct {
	Cluster
	Name string
}

func ServiceDelete(ctx context.Context, req ServiceDeleteRequest) error {
	out := req.Log()
	out.Command("service", "delete", req.Name)

	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	if err := kube.DeleteByName(ctx, ssh, names.KubeNamespace(), req.Name); err != nil {
		return err
	}
	out.Success("deleted")
	return nil
}
