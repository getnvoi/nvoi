package core

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type ServiceSetRequest struct {
	Cluster
	Output     Output
	Name       string
	Image      string
	Port       int
	Command    string
	Replicas   int
	EnvVars    []string // KEY=VALUE pairs (plain text in manifest)
	SvcSecrets []string // per-service secret refs → "{svc}-secrets" k8s Secret
	Volumes    []string // name:/path
	HealthPath string
	Servers    []string
}

func ServiceSet(ctx context.Context, req ServiceSetRequest) error {
	out := log(req.Output)

	if req.Image == "" {
		return ErrInput("--image is required")
	}

	names, err := req.Cluster.Names()
	if err != nil {
		return err
	}
	prov, err := req.Cluster.Compute()
	if err != nil {
		return err
	}

	ns := names.KubeNamespace()
	if err := req.Kube.EnsureNamespace(ctx, ns); err != nil {
		return err
	}

	// Resolve volumes — named volumes must exist as provider volumes
	managedVolPaths := map[string]string{}
	managed := false
	vols, _ := prov.ListVolumes(ctx, names.Labels())
	for _, mount := range req.Volumes {
		source, _, named, ok := utils.ParseVolumeMount(mount)
		if !ok {
			return ErrInputf("invalid volume mount %q", mount)
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
				return ErrNotFound("volume", source)
			}
		}
	}

	// Parse env vars
	var env []corev1.EnvVar
	for _, e := range req.EnvVars {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return ErrInputf("invalid env var %q — expected KEY=VALUE", e)
		}
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}

	spec := kube.ServiceSpec{
		Name:          req.Name,
		Image:         req.Image,
		Port:          req.Port,
		Command:       req.Command,
		Replicas:      req.Replicas,
		Env:           env,
		SvcSecrets:    req.SvcSecrets,
		SvcSecretName: names.KubeServiceSecrets(req.Name),
		Volumes:       req.Volumes,
		HealthPath:    req.HealthPath,
		Servers:       req.Servers,
		Managed:       managed,
	}

	out.Command("service", "set", req.Name)

	yaml, _, err := kube.GenerateYAML(spec, names, managedVolPaths)
	if err != nil {
		return fmt.Errorf("generate manifest: %w", err)
	}

	if err := req.Kube.Apply(ctx, ns, yaml); err != nil {
		return err
	}
	out.Success("applied")

	return nil
}

type ServiceDeleteRequest struct {
	Cluster
	Output Output
	Name   string
}

func ServiceDelete(ctx context.Context, req ServiceDeleteRequest) error {
	out := log(req.Output)
	out.Command("service", "delete", req.Name)

	names, err := req.Cluster.Names()
	if err != nil {
		return err
	}

	return req.Kube.DeleteByName(ctx, names.KubeNamespace(), req.Name)
}
