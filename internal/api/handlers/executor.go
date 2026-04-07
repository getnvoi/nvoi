package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/plan"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	"gorm.io/gorm"
)

// ExecuteParams holds everything the executor needs — loaded from the DB, not from env.
type ExecuteParams struct {
	Deployment *api.Deployment
	Repo       *api.Repo
	Config     *api.RepoConfig
	Env        map[string]string // decrypted RepoConfig.Env — provider credentials + app secrets
	GitToken   string            // user's GitHub token — from User.GithubToken, not from env
}

// executor holds deployment-scoped state: provider refs constructed once,
// builtImages accumulated across steps. Per-step args stay on the method.
type executor struct {
	db            *gorm.DB
	cluster       pkgcore.Cluster
	dns           pkgcore.ProviderRef
	storage       pkgcore.ProviderRef
	buildProvider string
	creds         map[string]string // build provider credentials (schema-mapped)
	gitToken      string            // user's GitHub token — for git clone auth during builds
	builtImages   map[string]string
}

func newExecutor(db *gorm.DB, p ExecuteParams) (*executor, error) {
	creds, err := resolveAllCredentials(p.Config, p.Env)
	if err != nil {
		return nil, err
	}

	return &executor{
		db: db,
		cluster: pkgcore.Cluster{
			AppName:     p.Repo.Name,
			Env:         p.Repo.Environment,
			Provider:    string(p.Config.ComputeProvider),
			Credentials: creds.Compute,
			SSHKey:      []byte(p.Repo.SSHPrivateKey),
		},
		dns:           pkgcore.ProviderRef{Name: string(p.Config.DNSProvider), Creds: creds.DNS},
		storage:       pkgcore.ProviderRef{Name: string(p.Config.StorageProvider), Creds: creds.Storage},
		buildProvider: string(p.Config.BuildProvider),
		creds:         creds.Build,
		gitToken:      p.GitToken,
		builtImages:   map[string]string{},
	}, nil
}

// Execute runs a deployment: walks steps in order, calls pkg/core/ functions,
// writes JSONL logs, updates statuses. Blocking — runs in a goroutine from the handler.
func Execute(ctx context.Context, db *gorm.DB, p ExecuteParams) {
	e, err := newExecutor(db, p)
	if err != nil {
		markDeploymentRunning(db, p.Deployment)
		markDeploymentDone(db, p.Deployment, err)
		return
	}
	e.run(ctx, p.Deployment)
}

// run walks steps for a deployment, dispatching each to step().
func (e *executor) run(ctx context.Context, deployment *api.Deployment) {
	e.cluster.EnableSSHCache()
	defer e.cluster.Close()

	markDeploymentRunning(e.db, deployment)

	var steps []api.DeploymentStep
	e.db.Where("deployment_id = ?", deployment.ID).Order("position").Find(&steps)

	var lastErr error
	hadServices := false
	for i := range steps {
		step := &steps[i]
		e.cluster.Output = newDBOutput(e.db, step.ID)
		markStepRunning(e.db, step)

		var params map[string]any
		if step.Params != "" {
			json.Unmarshal([]byte(step.Params), &params)
		}

		err := e.step(ctx, plan.StepKind(step.Kind), step.Name, params)
		markStepDone(e.db, step, err)

		if plan.StepKind(step.Kind) == plan.StepServiceSet {
			hadServices = true
		}

		if err != nil {
			lastErr = err
			skipRemainingSteps(e.db, deployment.ID)
			break
		}

		// After all services applied, wait for all pods to be ready.
		// This runs once: after the last service.set step, before dns.set.
		nextIsNotService := i+1 >= len(steps) || plan.StepKind(steps[i+1].Kind) != plan.StepServiceSet
		if hadServices && nextIsNotService && plan.StepKind(step.Kind) == plan.StepServiceSet {
			if err := pkgcore.WaitAllServices(ctx, pkgcore.WaitAllServicesRequest{Cluster: e.cluster}); err != nil {
				lastErr = err
				skipRemainingSteps(e.db, deployment.ID)
				break
			}
		}
	}

	markDeploymentDone(e.db, deployment, lastErr)
}

// step dispatches a single step to the corresponding pkg/core/ function.
func (e *executor) step(ctx context.Context, kind plan.StepKind, name string, params map[string]any) error {
	switch kind {
	case plan.StepFirewallSet:
		allowed, err := parseFirewallFromParams(params)
		if err != nil {
			return err
		}
		return pkgcore.FirewallSet(ctx, pkgcore.FirewallSetRequest{
			Cluster:    e.cluster,
			AllowedIPs: allowed,
		})

	case plan.StepComputeSet:
		_, err := pkgcore.ComputeSet(ctx, pkgcore.ComputeSetRequest{
			Cluster:    e.cluster,
			Name:       name,
			ServerType: utils.GetString(params, "type"),
			Region:     utils.GetString(params, "region"),
			Worker:     utils.GetBool(params, "worker"),
		})
		return err

	case plan.StepComputeDelete:
		return pkgcore.ComputeDelete(ctx, pkgcore.ComputeDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case plan.StepVolumeSet:
		_, err := pkgcore.VolumeSet(ctx, pkgcore.VolumeSetRequest{
			Cluster: e.cluster,
			Name:    name,
			Size:    utils.GetInt(params, "size"),
			Server:  utils.GetString(params, "server"),
		})
		return err

	case plan.StepVolumeDelete:
		return pkgcore.VolumeDelete(ctx, pkgcore.VolumeDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case plan.StepBuild:
		result, err := pkgcore.BuildRun(ctx, pkgcore.BuildRunRequest{
			Cluster:            e.cluster,
			Builder:            e.buildProvider,
			BuilderCredentials: e.creds,
			Source:             utils.GetString(params, "source"),
			Name:               name,
			GitToken:           e.gitToken,
		})
		if err != nil {
			return err
		}
		e.builtImages[name] = result.ImageRef
		return nil

	case plan.StepSecretSet:
		return pkgcore.SecretSet(ctx, pkgcore.SecretSetRequest{
			Cluster: e.cluster,
			Key:     name,
			Value:   utils.GetString(params, "value"),
		})

	case plan.StepSecretDelete:
		return pkgcore.SecretDelete(ctx, pkgcore.SecretDeleteRequest{
			Cluster: e.cluster,
			Key:     name,
		})

	case plan.StepStorageSet:
		return pkgcore.StorageSet(ctx, pkgcore.StorageSetRequest{
			Cluster:    e.cluster,
			Storage:    e.storage,
			Name:       name,
			Bucket:     utils.GetString(params, "bucket"),
			CORS:       utils.GetBool(params, "cors"),
			ExpireDays: utils.GetInt(params, "expire_days"),
		})

	case plan.StepStorageDelete:
		return pkgcore.StorageDelete(ctx, pkgcore.StorageDeleteRequest{
			Cluster: e.cluster,
			Storage: e.storage,
			Name:    name,
		})

	case plan.StepServiceSet:
		image := utils.GetString(params, "image")
		if buildRef := utils.GetString(params, "build"); buildRef != "" {
			if ref, ok := e.builtImages[buildRef]; ok {
				image = ref
			} else {
				ref, err := pkgcore.BuildLatest(ctx, pkgcore.BuildLatestRequest{
					Cluster: e.cluster,
					Name:    buildRef,
				})
				if err != nil {
					return fmt.Errorf("resolve image for build %q: %w", buildRef, err)
				}
				image = ref
				e.builtImages[buildRef] = ref
			}
		}

		return pkgcore.ServiceSet(ctx, pkgcore.ServiceSetRequest{
			Cluster:    e.cluster,
			Name:       name,
			Image:      image,
			Port:       utils.GetInt(params, "port"),
			Command:    utils.GetString(params, "command"),
			Replicas:   utils.GetInt(params, "replicas"),
			EnvVars:    utils.GetStringSlice(params, "env"),
			Secrets:    utils.GetStringSlice(params, "secrets"),
			Storages:   utils.GetStringSlice(params, "storage"),
			Volumes:    utils.GetStringSlice(params, "volumes"),
			HealthPath: utils.GetString(params, "health"),
			Server:     utils.GetString(params, "server"),
		})

	case plan.StepServiceDelete:
		return pkgcore.ServiceDelete(ctx, pkgcore.ServiceDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case plan.StepDNSSet:
		return pkgcore.DNSSet(ctx, pkgcore.DNSSetRequest{
			Cluster: e.cluster,
			DNS:     e.dns,
			Service: name,
			Domains: utils.GetStringSlice(params, "domains"),
		})

	case plan.StepIngressApply:
		routes, err := parseIngressRoutesFromParams(params)
		if err != nil {
			return err
		}
		return pkgcore.IngressApply(ctx, pkgcore.IngressApplyRequest{
			Cluster: e.cluster,
			Routes:  routes,
		})

	case plan.StepDNSDelete:
		return pkgcore.DNSDelete(ctx, pkgcore.DNSDeleteRequest{
			Cluster: e.cluster,
			DNS:     e.dns,
			Service: name,
			Domains: utils.GetStringSlice(params, "domains"),
		})

	default:
		return fmt.Errorf("unknown step kind: %s", kind)
	}
}

// ── firewall param parsing ─────────────────────────────────────────────────────

// parseFirewallFromParams converts step params into a PortAllowList.
// Supports "preset" key (resolved via ResolveFirewallArgs) and/or "rules" map.
func parseFirewallFromParams(params map[string]any) (provider.PortAllowList, error) {
	preset := utils.GetString(params, "preset")
	rulesRaw, hasRules := params["rules"]

	// Build args for ResolveFirewallArgs
	var args []string
	if preset != "" {
		args = append(args, preset)
	}
	if hasRules {
		// Rules is map[string][]string (port → CIDRs)
		if rulesMap, ok := rulesRaw.(map[string]any); ok {
			for port, v := range rulesMap {
				if cidrs, ok := v.([]any); ok {
					for _, cidr := range cidrs {
						if s, ok := cidr.(string); ok {
							args = append(args, fmt.Sprintf("%s:%s", port, s))
						}
					}
				}
			}
		}
		if rulesMap, ok := rulesRaw.(map[string][]string); ok {
			for port, cidrs := range rulesMap {
				for _, cidr := range cidrs {
					args = append(args, fmt.Sprintf("%s:%s", port, cidr))
				}
			}
		}
	}

	return provider.ResolveFirewallArgs(context.Background(), args)
}

// parseIngressRoutesFromParams extracts routes from step params.
func parseIngressRoutesFromParams(params map[string]any) ([]pkgcore.IngressRouteArg, error) {
	routesRaw, ok := params["routes"]
	if !ok {
		return nil, fmt.Errorf("ingress.apply: missing routes param")
	}
	routesList, ok := routesRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("ingress.apply: routes must be a list")
	}
	var routes []pkgcore.IngressRouteArg
	for _, item := range routesList {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		svc := utils.GetString(m, "service")
		domains := utils.GetStringSlice(m, "domains")
		if svc != "" && len(domains) > 0 {
			routes = append(routes, pkgcore.IngressRouteArg{Service: svc, Domains: domains})
		}
	}
	return routes, nil
}

// ── status helpers ─────────────────────────────────────────────────────────────

func markStepRunning(db *gorm.DB, step *api.DeploymentStep) {
	now := time.Now()
	db.Model(step).Updates(map[string]any{
		"status":     api.StepStatusRunning,
		"started_at": &now,
	})
}

func markStepDone(db *gorm.DB, step *api.DeploymentStep, err error) {
	now := time.Now()
	if err != nil {
		db.Model(step).Updates(map[string]any{
			"status":      api.StepStatusFailed,
			"error":       err.Error(),
			"finished_at": &now,
		})
	} else {
		db.Model(step).Updates(map[string]any{
			"status":      api.StepStatusSucceeded,
			"finished_at": &now,
		})
	}
}

func markDeploymentRunning(db *gorm.DB, deployment *api.Deployment) {
	now := time.Now()
	db.Model(deployment).Updates(map[string]any{
		"status":     api.DeploymentRunning,
		"started_at": &now,
	})
}

func markDeploymentDone(db *gorm.DB, deployment *api.Deployment, err error) {
	now := time.Now()
	status := api.DeploymentSucceeded
	if err != nil {
		status = api.DeploymentFailed
	}
	db.Model(deployment).Updates(map[string]any{
		"status":      status,
		"finished_at": &now,
	})
}

func skipRemainingSteps(db *gorm.DB, deploymentID string) {
	db.Model(&api.DeploymentStep{}).
		Where("deployment_id = ? AND status = ?", deploymentID, api.StepStatusPending).
		Update("status", api.StepStatusSkipped)
}
