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
	// New path: read provider credentials from InfraProvider records on Repo.
	// Fallback: legacy path reads from RepoConfig env + provider enum columns.
	if p.Repo.ComputeProviderID != nil {
		return newExecutorFromProviders(db, p)
	}
	return newExecutorLegacy(db, p)
}

// newExecutorFromProviders builds the executor using InfraProvider credentials.
func newExecutorFromProviders(db *gorm.DB, p ExecuteParams) (*executor, error) {
	repo := p.Repo

	computeName, computeCreds := "", map[string]string(nil)
	if repo.ComputeProvider != nil {
		computeName = repo.ComputeProvider.Name
		computeCreds = repo.ComputeProvider.CredentialsMap()
	}

	dnsName, dnsCreds := "", map[string]string(nil)
	if repo.DNSProvider != nil {
		dnsName = repo.DNSProvider.Name
		dnsCreds = repo.DNSProvider.CredentialsMap()
	}

	storageName, storageCreds := "", map[string]string(nil)
	if repo.StorageProvider != nil {
		storageName = repo.StorageProvider.Name
		storageCreds = repo.StorageProvider.CredentialsMap()
	}

	buildName, buildCreds := "", map[string]string(nil)
	if repo.BuildProvider != nil {
		buildName = repo.BuildProvider.Name
		buildCreds = repo.BuildProvider.CredentialsMap()
	}

	return &executor{
		db: db,
		cluster: pkgcore.Cluster{
			AppName:     repo.Name,
			Env:         repo.Environment,
			Provider:    computeName,
			Credentials: computeCreds,
			SSHKey:      []byte(repo.SSHPrivateKey),
		},
		dns:           pkgcore.ProviderRef{Name: dnsName, Creds: dnsCreds},
		storage:       pkgcore.ProviderRef{Name: storageName, Creds: storageCreds},
		buildProvider: buildName,
		creds:         buildCreds,
		gitToken:      p.GitToken,
		builtImages:   map[string]string{},
	}, nil
}

// newExecutorLegacy builds the executor from RepoConfig env + provider columns (migration path).
func newExecutorLegacy(db *gorm.DB, p ExecuteParams) (*executor, error) {
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
	var pendingRollouts []pkgcore.WaitRolloutRequest
	for i := range steps {
		step := &steps[i]
		e.cluster.Output = newDBOutput(e.db, step.ID)
		markStepRunning(e.db, step)

		var params map[string]any
		if step.Params != "" {
			if err := json.Unmarshal([]byte(step.Params), &params); err != nil {
				markStepDone(e.db, step, err)
				lastErr = fmt.Errorf("parse step params: %w", err)
				skipRemainingSteps(e.db, deployment.ID)
				break
			}
		}

		err := e.step(ctx, plan.StepKind(step.Kind), step.Name, params)
		markStepDone(e.db, step, err)

		if err != nil {
			lastErr = err
			skipRemainingSteps(e.db, deployment.ID)
			break
		}

		// Collect service rollout info for parallel wait after all services applied.
		if plan.StepKind(step.Kind) == plan.StepServiceSet {
			kind := "deployment"
			if len(utils.GetStringSlice(params, "volumes")) > 0 {
				kind = "statefulset"
			}
			pendingRollouts = append(pendingRollouts, pkgcore.WaitRolloutRequest{
				Cluster:        e.cluster,
				Service:        step.Name,
				WorkloadKind:   kind,
				HasHealthCheck: utils.GetString(params, "health") != "",
			})
		}

		// After last service.set step, wait for all rollouts in parallel.
		nextIsNotService := i+1 >= len(steps) || plan.StepKind(steps[i+1].Kind) != plan.StepServiceSet
		if len(pendingRollouts) > 0 && nextIsNotService {
			if err := waitRolloutsParallel(ctx, pendingRollouts); err != nil {
				lastErr = err
				skipRemainingSteps(e.db, deployment.ID)
				break
			}
			pendingRollouts = nil
		}
	}

	markDeploymentDone(e.db, deployment, lastErr)
}

// waitRolloutsParallel waits for all service rollouts concurrently.
// First failure cancels all others.
func waitRolloutsParallel(ctx context.Context, rollouts []pkgcore.WaitRolloutRequest) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, len(rollouts))
	for _, req := range rollouts {
		req := req
		go func() {
			errs <- pkgcore.WaitRollout(ctx, req)
		}()
	}

	var firstErr error
	for range rollouts {
		if err := <-errs; err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	return firstErr
}

// step dispatches a single step to the corresponding pkg/core/ function.
func (e *executor) step(ctx context.Context, kind plan.StepKind, name string, params map[string]any) error {
	switch kind {
	case plan.StepFirewallSet:
		allowed, err := parseFirewallFromParams(ctx, params)
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
			Worker:     utils.GetString(params, "role") == "worker",
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
		image, err := e.resolveImage(ctx, params)
		if err != nil {
			return err
		}

		return pkgcore.ServiceSet(ctx, pkgcore.ServiceSetRequest{
			Cluster:     e.cluster,
			Name:        name,
			Image:       image,
			Port:        utils.GetInt(params, "port"),
			Command:     utils.GetString(params, "command"),
			Replicas:    utils.GetInt(params, "replicas"),
			EnvVars:     utils.GetStringSlice(params, "env"),
			Secrets:     utils.GetStringSlice(params, "secrets"),
			Storages:    utils.GetStringSlice(params, "storage"),
			Volumes:     utils.GetStringSlice(params, "volumes"),
			HealthPath:  utils.GetString(params, "health"),
			Server:      utils.GetString(params, "server"),
			ManagedKind: utils.GetString(params, "managed_kind"),
		})

	case plan.StepServiceDelete:
		return pkgcore.ServiceDelete(ctx, pkgcore.ServiceDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case plan.StepCronSet:
		image, err := e.resolveImage(ctx, params)
		if err != nil {
			return err
		}
		return pkgcore.CronSet(ctx, pkgcore.CronSetRequest{
			Cluster:  e.cluster,
			Name:     name,
			Image:    image,
			Command:  utils.GetString(params, "command"),
			EnvVars:  utils.GetStringSlice(params, "env"),
			Secrets:  utils.GetStringSlice(params, "secrets"),
			Storages: utils.GetStringSlice(params, "storage"),
			Volumes:  utils.GetStringSlice(params, "volumes"),
			Schedule: utils.GetString(params, "schedule"),
			Server:   utils.GetString(params, "server"),
		})

	case plan.StepCronDelete:
		return pkgcore.CronDelete(ctx, pkgcore.CronDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case plan.StepDNSSet:
		return pkgcore.DNSSet(ctx, pkgcore.DNSSetRequest{
			Cluster:           e.cluster,
			DNS:               e.dns,
			Service:           name,
			Domains:           utils.GetStringSlice(params, "domains"),
			CloudflareManaged: utils.GetBool(params, "cloudflare_managed"),
		})

	case plan.StepIngressSet:
		return pkgcore.IngressSet(ctx, pkgcore.IngressSetRequest{
			Cluster: e.cluster,
			DNS:     e.dns,
			Route: pkgcore.IngressRouteArg{
				Service: utils.GetString(params, "service"),
				Domains: utils.GetStringSlice(params, "domains"),
			},
			CloudflareManaged: utils.GetBool(params, "cloudflare_managed"),
			CertPEM:           utils.GetString(params, "cert_pem"),
			KeyPEM:            utils.GetString(params, "key_pem"),
		})

	case plan.StepIngressDelete:
		return pkgcore.IngressDelete(ctx, pkgcore.IngressDeleteRequest{
			Cluster: e.cluster,
			DNS:     e.dns,
			Route: pkgcore.IngressRouteArg{
				Service: utils.GetString(params, "service"),
				Domains: utils.GetStringSlice(params, "domains"),
			},
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
// resolveImage resolves the image from step params — uses build target if set,
// caches results in builtImages.
func (e *executor) resolveImage(ctx context.Context, params map[string]any) (string, error) {
	image := utils.GetString(params, "image")
	buildRef := utils.GetString(params, "build")
	if buildRef == "" {
		return image, nil
	}
	if ref, ok := e.builtImages[buildRef]; ok {
		return ref, nil
	}
	ref, err := pkgcore.BuildLatest(ctx, pkgcore.BuildLatestRequest{
		Cluster: e.cluster,
		Name:    buildRef,
	})
	if err != nil {
		return "", fmt.Errorf("resolve image for build %q: %w", buildRef, err)
	}
	e.builtImages[buildRef] = ref
	return ref, nil
}

func parseFirewallFromParams(ctx context.Context, params map[string]any) (provider.PortAllowList, error) {
	if params == nil {
		return nil, nil
	}
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

	return provider.ResolveFirewallArgs(ctx, args)
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
