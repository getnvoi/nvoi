package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type CronSetRequest struct {
	Cluster
	Name       string
	Image      string
	Command    string
	EnvVars    []string
	Secrets    []string
	SvcSecrets []string // per-cron secret refs → "{cron}-secrets" k8s Secret
	Storages   []string
	Volumes    []string
	Schedule   string
	Servers    []string
}

func CronSet(ctx context.Context, req CronSetRequest) error {
	out := req.Log()

	master, names, prov, err := req.Cluster.Master(ctx)
	if err != nil {
		return err
	}

	addr := master.IPv4 + ":22"
	connectFn := req.Cluster.SSHFunc
	if connectFn == nil {
		connectFn = func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return infra.ConnectSSH(ctx, addr, utils.DefaultUser, req.SSHKey)
		}
	}
	ssh, err := connectFn(ctx, addr)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
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

	for _, storageName := range req.Storages {
		for _, key := range StorageSecretKeys(storageName) {
			req.Secrets = append(req.Secrets, key)
		}
	}

	secretName := names.KubeSecrets()
	if len(req.Secrets) > 0 {
		existing, err := kube.ListSecretKeys(ctx, ssh, ns, secretName)
		if err != nil {
			return fmt.Errorf("cannot verify secrets — run 'secret set' first: %w", err)
		}
		for _, ref := range req.Secrets {
			_, secretKey := kube.ParseSecretRef(ref)
			found := false
			for _, k := range existing {
				if k == secretKey {
					found = true
					break
				}
			}
			if !found {
				return ErrNotFound("secret", secretKey)
			}
		}
	}

	out.Command("cron", "set", req.Name)
	yaml, err := kube.GenerateCronYAML(kube.CronSpec{
		Name:          req.Name,
		Schedule:      req.Schedule,
		Image:         req.Image,
		Command:       req.Command,
		Env:           env,
		Secrets:       req.Secrets,
		SecretName:    secretName,
		SvcSecrets:    req.SvcSecrets,
		SvcSecretName: names.KubeServiceSecrets(req.Name),
		Volumes:       req.Volumes,
		Servers:       req.Servers,
	}, names, managedVolPaths)
	if err != nil {
		return fmt.Errorf("generate manifest: %w", err)
	}
	if err := kube.Apply(ctx, ssh, ns, yaml); err != nil {
		return err
	}
	out.Success("applied")
	return nil
}

type CronRunRequest struct {
	Cluster
	Name string
}

func CronRun(ctx context.Context, req CronRunRequest) error {
	out := req.Log()

	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	jobName := fmt.Sprintf("%s-run-%d", req.Name, time.Now().Unix())

	out.Command("cron", "run", req.Name, "job", jobName)

	if err := kube.CreateJobFromCronJob(ctx, ssh, ns, req.Name, jobName); err != nil {
		return err
	}
	out.Progress("waiting for completion")

	if err := kube.WaitForJob(ctx, ssh, ns, jobName, out); err != nil {
		return err
	}

	// Stream logs
	logs := kube.RecentLogs(ctx, ssh, ns, jobName, "", 50)
	if logs != "" {
		w := out.Writer()
		fmt.Fprintln(w, logs)
	}

	out.Success("completed")
	return nil
}

type CronDeleteRequest struct {
	Cluster
	Name string
}

func CronDelete(ctx context.Context, req CronDeleteRequest) error {
	out := req.Log()
	out.Command("cron", "delete", req.Name)

	ssh, names, err := req.Cluster.SSH(ctx)
	if errors.Is(err, ErrNoMaster) {
		return ErrNoMaster
	}
	if err != nil {
		return err
	}
	defer ssh.Close()

	return kube.DeleteCronByName(ctx, ssh, names.KubeNamespace(), req.Name)
}
