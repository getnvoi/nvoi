package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type CronSetRequest struct {
	Cluster
	Output     Output
	Name       string
	Image      string
	Command    string
	EnvVars    []string
	SvcSecrets []string // per-cron secret refs → "{cron}-secrets" k8s Secret
	Volumes    []string
	Schedule   string
	Servers    []string
}

func CronSet(ctx context.Context, req CronSetRequest) error {
	out := log(req.Output)

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

	managedVolPaths := map[string]string{}
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
					found = true
					break
				}
			}
			if !found {
				return ErrNotFound("volume", source)
			}
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
	yaml, err := kube.GenerateCronYAML(kube.CronSpec{
		Name:          req.Name,
		Schedule:      req.Schedule,
		Image:         req.Image,
		Command:       req.Command,
		Env:           env,
		SvcSecrets:    req.SvcSecrets,
		SvcSecretName: names.KubeServiceSecrets(req.Name),
		Volumes:       req.Volumes,
		Servers:       req.Servers,
	}, names, managedVolPaths)
	if err != nil {
		return fmt.Errorf("generate manifest: %w", err)
	}
	if err := req.Kube.Apply(ctx, ns, yaml); err != nil {
		return err
	}
	out.Success("applied")
	return nil
}

type CronRunRequest struct {
	Cluster
	Output Output
	Name   string
}

func CronRun(ctx context.Context, req CronRunRequest) error {
	out := log(req.Output)

	names, err := req.Cluster.Names()
	if err != nil {
		return err
	}

	ns := names.KubeNamespace()
	jobName := fmt.Sprintf("%s-run-%d", req.Name, time.Now().Unix())

	out.Command("cron", "run", req.Name, "job", jobName)

	if err := req.Kube.CreateJobFromCronJob(ctx, ns, req.Name, jobName); err != nil {
		return err
	}
	out.Progress("waiting for completion")

	if err := req.Kube.WaitForJob(ctx, ns, jobName, out); err != nil {
		return err
	}

	// Stream logs
	logs := req.Kube.RecentLogs(ctx, ns, jobName, 50)
	if logs != "" {
		w := out.Writer()
		fmt.Fprintln(w, logs)
	}

	out.Success("completed")
	return nil
}

type CronDeleteRequest struct {
	Cluster
	Output Output
	Name   string
}

func CronDelete(ctx context.Context, req CronDeleteRequest) error {
	out := log(req.Output)
	out.Command("cron", "delete", req.Name)

	names, err := req.Cluster.Names()
	if err != nil {
		return err
	}

	return req.Kube.DeleteCronByName(ctx, names.KubeNamespace(), req.Name)
}
