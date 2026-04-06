package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
	"gorm.io/gorm"
)

// ExecuteParams holds everything the executor needs — loaded from the DB, not from env.
type ExecuteParams struct {
	Deployment *api.Deployment
	Repo       *api.Repo
	Config     *api.RepoConfig
	Env        map[string]string // decrypted RepoConfig.Env — provider credentials + app secrets only
}

// executor holds deployment-scoped state: provider refs constructed once,
// builtImages accumulated across steps. Per-step args stay on the method.
type executor struct {
	db            *gorm.DB
	cluster       pkgcore.Cluster
	dns           pkgcore.ProviderRef
	storage       pkgcore.ProviderRef
	buildProvider string
	creds         map[string]string
	builtImages   map[string]string
}

func newExecutor(db *gorm.DB, p ExecuteParams) *executor {
	return &executor{
		db: db,
		cluster: pkgcore.Cluster{
			AppName:     p.Repo.Name,
			Env:         p.Repo.Environment,
			Provider:    string(p.Config.ComputeProvider),
			Credentials: p.Env,
			SSHKey:      []byte(p.Repo.SSHPrivateKey),
		},
		dns:           pkgcore.ProviderRef{Name: string(p.Config.DNSProvider), Creds: p.Env},
		storage:       pkgcore.ProviderRef{Name: string(p.Config.StorageProvider), Creds: p.Env},
		buildProvider: string(p.Config.BuildProvider),
		creds:         p.Env,
		builtImages:   map[string]string{},
	}
}

// Execute runs a deployment: walks steps in order, calls pkg/core/ functions,
// writes JSONL logs, updates statuses. Blocking — runs in a goroutine from the handler.
func Execute(ctx context.Context, db *gorm.DB, p ExecuteParams) {
	e := newExecutor(db, p)
	e.run(ctx, p.Deployment)
}

// run walks steps for a deployment, dispatching each to step().
func (e *executor) run(ctx context.Context, deployment *api.Deployment) {
	markDeploymentRunning(e.db, deployment)

	var steps []api.DeploymentStep
	e.db.Where("deployment_id = ?", deployment.ID).Order("position").Find(&steps)

	var lastErr error
	for i := range steps {
		step := &steps[i]
		e.cluster.Output = newDBOutput(e.db, step.ID)
		markStepRunning(e.db, step)

		var params map[string]any
		if step.Params != "" {
			json.Unmarshal([]byte(step.Params), &params)
		}

		err := e.step(ctx, config.StepKind(step.Kind), step.Name, params)
		markStepDone(e.db, step, err)

		if err != nil {
			lastErr = err
			skipRemainingSteps(e.db, deployment.ID)
			break
		}
	}

	markDeploymentDone(e.db, deployment, lastErr)
}

// step dispatches a single step to the corresponding pkg/core/ function.
func (e *executor) step(ctx context.Context, kind config.StepKind, name string, params map[string]any) error {
	switch kind {
	case config.StepComputeSet:
		_, err := pkgcore.ComputeSet(ctx, pkgcore.ComputeSetRequest{
			Cluster:    e.cluster,
			Name:       name,
			ServerType: utils.GetString(params, "type"),
			Region:     utils.GetString(params, "region"),
			Worker:     utils.GetBool(params, "worker"),
		})
		return err

	case config.StepComputeDelete:
		return pkgcore.ComputeDelete(ctx, pkgcore.ComputeDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case config.StepVolumeSet:
		_, err := pkgcore.VolumeSet(ctx, pkgcore.VolumeSetRequest{
			Cluster: e.cluster,
			Name:    name,
			Size:    utils.GetInt(params, "size"),
			Server:  utils.GetString(params, "server"),
		})
		return err

	case config.StepVolumeDelete:
		return pkgcore.VolumeDelete(ctx, pkgcore.VolumeDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case config.StepBuild:
		result, err := pkgcore.BuildRun(ctx, pkgcore.BuildRunRequest{
			Cluster:            e.cluster,
			Builder:            e.buildProvider,
			BuilderCredentials: e.creds,
			Source:             utils.GetString(params, "source"),
			Name:               name,
			GitToken:           e.creds["GITHUB_TOKEN"],
		})
		if err != nil {
			return err
		}
		e.builtImages[name] = result.ImageRef
		return nil

	case config.StepSecretSet:
		return pkgcore.SecretSet(ctx, pkgcore.SecretSetRequest{
			Cluster: e.cluster,
			Key:     name,
			Value:   utils.GetString(params, "value"),
		})

	case config.StepSecretDelete:
		return pkgcore.SecretDelete(ctx, pkgcore.SecretDeleteRequest{
			Cluster: e.cluster,
			Key:     name,
		})

	case config.StepStorageSet:
		return pkgcore.StorageSet(ctx, pkgcore.StorageSetRequest{
			Cluster:    e.cluster,
			Storage:    e.storage,
			Name:       name,
			Bucket:     utils.GetString(params, "bucket"),
			CORS:       utils.GetBool(params, "cors"),
			ExpireDays: utils.GetInt(params, "expire_days"),
		})

	case config.StepStorageDelete:
		return pkgcore.StorageDelete(ctx, pkgcore.StorageDeleteRequest{
			Cluster: e.cluster,
			Storage: e.storage,
			Name:    name,
		})

	case config.StepServiceSet:
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

	case config.StepServiceDelete:
		return pkgcore.ServiceDelete(ctx, pkgcore.ServiceDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case config.StepDNSSet:
		return pkgcore.DNSSet(ctx, pkgcore.DNSSetRequest{
			Cluster: e.cluster,
			DNS:     e.dns,
			Service: name,
			Domains: utils.GetStringSlice(params, "domains"),
		})

	case config.StepDNSDelete:
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
