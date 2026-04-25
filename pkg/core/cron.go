package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type CronSetRequest struct {
	Cluster
	Cfg            provider.ProviderConfigView // forwards to Cluster.Kube for on-demand connect
	Name           string
	Image          string
	Command        string
	EnvVars        []string
	SvcSecrets     []string // per-cron secret refs → "{cron}-secrets" k8s Secret
	Volumes        []string
	Schedule       string
	Servers        []string
	PullSecretName string // optional imagePullSecrets target; empty = pull as anonymous

	// KnownVolumes is the set of provider-managed volume short-names the
	// caller has verified exist at the provider (mirror of the same field
	// on ServiceSetRequest). Reconcile.Crons populates from
	// infra.LiveSnapshot.Volumes.
	KnownVolumes []string
}

func CronSet(ctx context.Context, req CronSetRequest) error {
	out := req.Log()

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

	knownSet := make(map[string]bool, len(req.KnownVolumes))
	for _, v := range req.KnownVolumes {
		knownSet[v] = true
	}
	managedVolPaths := map[string]string{}
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
		}
	}

	var env []corev1.EnvVar
	for _, e := range req.EnvVars {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return ErrInputf("invalid env var %q — expected KEY=VALUE", e)
		}
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}

	out.Command("cron", "set", req.Name)
	cron, err := kube.BuildCronJob(kube.CronSpec{
		Name:           req.Name,
		Schedule:       req.Schedule,
		Image:          req.Image,
		Command:        req.Command,
		Env:            env,
		SvcSecrets:     req.SvcSecrets,
		SvcSecretName:  names.KubeServiceSecrets(req.Name),
		Volumes:        req.Volumes,
		Servers:        req.Servers,
		PullSecretName: req.PullSecretName,
		DeployHash:     req.Cluster.DeployHash,
	}, names, managedVolPaths)
	if err != nil {
		return fmt.Errorf("build cronjob: %w", err)
	}
	if err := kc.ApplyOwned(ctx, ns, utils.OwnerCrons, cron); err != nil {
		return err
	}
	out.Success("applied")
	return nil
}

type CronRunRequest struct {
	Cluster
	Cfg  provider.ProviderConfigView
	Name string
}

func CronRun(ctx context.Context, req CronRunRequest) error {
	out := req.Log()

	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	ns := names.KubeNamespace()
	jobName := names.CronJobRunName(req.Name, time.Now().Unix())

	out.Command("cron", "run", req.Name, "job", jobName)

	if err := kc.CreateJobFromCronJob(ctx, ns, req.Name, jobName); err != nil {
		return err
	}
	out.Progress("waiting for completion")

	if err := kc.WaitForJob(ctx, ns, jobName, out); err != nil {
		return err
	}

	logs := kc.RecentLogs(ctx, ns, jobName, "", 50)
	if logs != "" {
		w := out.Writer()
		fmt.Fprintln(w, logs)
	}

	out.Success("completed")
	return nil
}

type CronDeleteRequest struct {
	Cluster
	Cfg  provider.ProviderConfigView
	Name string
}

func CronDelete(ctx context.Context, req CronDeleteRequest) error {
	out := req.Log()
	out.Command("cron", "delete", req.Name)

	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if errors.Is(err, ErrNoMaster) {
		return ErrNoMaster
	}
	if err != nil {
		return err
	}
	defer cleanup()

	return kc.DeleteCronByName(ctx, names.KubeNamespace(), req.Name)
}
