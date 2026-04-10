package reconcile

import (
	"context"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Services(ctx context.Context, dc *DeployContext, live *LiveState, cfg *AppConfig) error {
	svcNames := utils.SortedKeys(cfg.Services)
	for i, name := range svcNames {
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
		if err := app.ServiceSet(ctx, app.ServiceSetRequest{
			Cluster: dc.Cluster, Name: name, Image: image,
			Port: svc.Port, Command: svc.Command, Replicas: replicas,
			EnvVars: svc.Env, Secrets: svc.Secrets, Storages: svc.Storage,
			Volumes: svc.Volumes, HealthPath: svc.Health, Servers: servers,
		}); err != nil {
			return err
		}
		if i == len(svcNames)-1 {
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
	}

	if live != nil {
		desired := toSet(svcNames)
		for _, name := range live.Services {
			if !desired[name] && name != "caddy" {
				_ = app.ServiceDelete(ctx, app.ServiceDeleteRequest{Cluster: dc.Cluster, Name: name})
			}
		}
	}
	return nil
}
