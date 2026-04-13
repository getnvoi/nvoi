package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Services(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig, packageEnvVars map[string]string) error {
	svcNames := utils.SortedKeys(cfg.Services)
	for _, name := range svcNames {
		svc := cfg.Services[name]
		image, err := resolveImageRef(ctx, dc, svc.Image, svc.Build)
		if err != nil {
			return err
		}
		servers := ResolveServers(cfg, svc.Servers, svc.Server, svc.Volumes)
		replicas := svc.Replicas
		if _, hasDomain := cfg.Domains[name]; hasDomain && replicas == 0 {
			replicas = 2
		}
		// Merge package env vars (database, etc.) into service env
		envVars := append([]string{}, svc.Env...)
		for k, v := range packageEnvVars {
			envVars = append(envVars, k+"="+v)
		}
		if err := app.ServiceSet(ctx, app.ServiceSetRequest{
			Cluster: dc.Cluster, Name: name, Image: image,
			Port: svc.Port, Command: svc.Command, Replicas: replicas,
			EnvVars: envVars, Secrets: svc.Secrets, Storages: svc.Storage,
			Volumes: svc.Volumes, HealthPath: svc.Health, Servers: servers,
		}); err != nil {
			return err
		}
		kind := "deployment"
		if len(svc.Volumes) > 0 {
			kind = "statefulset"
		}
		if err := app.WaitRollout(ctx, app.WaitRolloutRequest{
			Cluster: dc.Cluster, Service: name,
			WorkloadKind: kind, HasHealthCheck: svc.Health != "",
		}); err != nil {
			return err
		}
	}

	if live != nil {
		desired := toSet(svcNames)
		// Exclude package-managed services from orphan detection
		protected := map[string]bool{}
		for dbName := range cfg.Database {
			protected[dbName+"-db"] = true
		}
		for _, name := range live.Services {
			if !desired[name] && !protected[name] {
				if err := app.ServiceDelete(ctx, app.ServiceDeleteRequest{Cluster: dc.Cluster, Name: name}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan service %s not removed: %s", name, err))
				}
			}
		}
	}
	return nil
}
