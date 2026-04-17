package core

import (
	"context"
	"errors"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type ServiceSetRequest struct {
	Cluster
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
	out := req.Log()

	if req.Image == "" {
		return ErrInput("--image is required")
	}

	_, names, prov, err := req.Cluster.Master(ctx)
	if err != nil {
		return err
	}

	kc, _, cleanup, err := req.Cluster.Kube(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	ns := names.KubeNamespace()
	if err := kc.EnsureNamespace(ctx, ns); err != nil {
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

	workload, svc, _, err := kube.BuildService(spec, names, managedVolPaths)
	if err != nil {
		return err
	}
	if err := kc.Apply(ctx, ns, workload); err != nil {
		return err
	}
	if err := kc.Apply(ctx, ns, svc); err != nil {
		return err
	}
	out.Success("applied")

	return nil
}

type ServiceDeleteRequest struct {
	Cluster
	Name string
}

func ServiceDelete(ctx context.Context, req ServiceDeleteRequest) error {
	out := req.Log()
	out.Command("service", "delete", req.Name)

	kc, names, cleanup, err := req.Cluster.Kube(ctx)
	if errors.Is(err, ErrNoMaster) {
		return ErrNoMaster
	}
	if err != nil {
		return err
	}
	defer cleanup()

	return kc.DeleteByName(ctx, names.KubeNamespace(), req.Name)
}
