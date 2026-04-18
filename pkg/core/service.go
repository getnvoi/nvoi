package core

import (
	"context"
	"errors"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type ServiceSetRequest struct {
	Cluster
	Cfg            provider.ProviderConfigView // forwards to Cluster.Kube for on-demand connect
	Name           string
	Image          string
	Port           int
	Command        string
	Replicas       int
	EnvVars        []string // KEY=VALUE pairs (plain text in manifest)
	SvcSecrets     []string // per-service secret refs → "{svc}-secrets" k8s Secret
	Volumes        []string // name:/path
	HealthPath     string
	Servers        []string
	PullSecretName string // optional imagePullSecrets target; empty = pull as anonymous

	// KnownVolumes is the set of provider-managed volume short-names
	// (config keys, NOT prefixed names) the caller has already verified
	// exist at the provider. ServiceSet validates that every named volume
	// mount references one of these — no provider call here, the caller
	// (reconcile.Services) populates it from infra.LiveSnapshot.Volumes.
	KnownVolumes []string
}

func ServiceSet(ctx context.Context, req ServiceSetRequest) error {
	out := req.Log()

	if req.Image == "" {
		return ErrInput("--image is required")
	}

	names, err := req.Cluster.Names()
	if err != nil {
		return err
	}

	kc, _, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	ns := names.KubeNamespace()

	// Resolve volumes — named volumes must be in the caller's KnownVolumes
	// list (populated from infra.LiveSnapshot). The provider isn't asked
	// here; that lookup is the reconciler's job.
	knownSet := make(map[string]bool, len(req.KnownVolumes))
	for _, v := range req.KnownVolumes {
		knownSet[v] = true
	}
	managedVolPaths := map[string]string{}
	managed := false
	for _, mount := range req.Volumes {
		source, _, named, ok := utils.ParseVolumeMount(mount)
		if !ok {
			return ErrInputf("invalid volume mount %q", mount)
		}
		if named {
			if !knownSet[source] {
				return ErrNotFound("volume", source)
			}
			managedVolPaths[source] = names.VolumeMountPath(source)
			managed = true
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
		Name:           req.Name,
		Image:          req.Image,
		Port:           req.Port,
		Command:        req.Command,
		Replicas:       req.Replicas,
		Env:            env,
		SvcSecrets:     req.SvcSecrets,
		SvcSecretName:  names.KubeServiceSecrets(req.Name),
		Volumes:        req.Volumes,
		HealthPath:     req.HealthPath,
		Servers:        req.Servers,
		Managed:        managed,
		PullSecretName: req.PullSecretName,
		DeployHash:     req.Cluster.DeployHash,
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
	Cfg  provider.ProviderConfigView
	Name string
}

func ServiceDelete(ctx context.Context, req ServiceDeleteRequest) error {
	out := req.Log()
	out.Command("service", "delete", req.Name)

	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if errors.Is(err, ErrNoMaster) {
		return ErrNoMaster
	}
	if err != nil {
		return err
	}
	defer cleanup()

	return kc.DeleteByName(ctx, names.KubeNamespace(), req.Name)
}
