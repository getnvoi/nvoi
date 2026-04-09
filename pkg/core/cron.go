package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type CronSetRequest struct {
	Cluster
	Name     string
	Image    string
	Command  string
	EnvVars  []string
	Secrets  []string
	Storages []string
	Volumes  []string
	Schedule string
	Server   string
}

func CronSet(ctx context.Context, req CronSetRequest) error {
	out := req.Log()

	if req.Image == "" {
		return fmt.Errorf("--image is required")
	}
	if err := validateCronSchedule(req.Schedule); err != nil {
		return err
	}

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
			return fmt.Errorf("invalid volume mount %q", mount)
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
				return fmt.Errorf("volume %q not found", source)
			}
		}
	}

	var env []corev1.EnvVar
	for _, e := range req.EnvVars {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return fmt.Errorf("invalid env var %q — expected KEY=VALUE", e)
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
				return fmt.Errorf("secret %q not found — run 'nvoi secret set %s <value>' first", secretKey, secretKey)
			}
		}
	}

	out.Command("cron", "set", req.Name)
	yaml, err := kube.GenerateCronYAML(kube.CronSpec{
		Name:       req.Name,
		Schedule:   req.Schedule,
		Image:      req.Image,
		Command:    req.Command,
		Env:        env,
		Secrets:    req.Secrets,
		SecretName: secretName,
		Volumes:    req.Volumes,
		Server:     req.Server,
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

func validateCronSchedule(schedule string) error {
	schedule = strings.TrimSpace(schedule)
	if schedule == "" {
		return fmt.Errorf("--schedule is required")
	}
	if strings.HasPrefix(schedule, "@") {
		switch schedule {
		case "@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly":
			return nil
		default:
			return fmt.Errorf("invalid cron schedule %q", schedule)
		}
	}
	if len(strings.Fields(schedule)) != 5 {
		return fmt.Errorf("invalid cron schedule %q", schedule)
	}
	return nil
}
