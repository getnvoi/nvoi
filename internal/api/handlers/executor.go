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

// Execute runs a deployment: walks steps in order, calls pkg/core/ functions,
// writes JSONL logs, updates statuses. Blocking — runs in a goroutine from the handler.
func Execute(ctx context.Context, db *gorm.DB, p ExecuteParams) {
	markDeploymentRunning(db, p.Deployment)

	// Build Cluster from DB fields — never from env string lookups.
	cluster := pkgcore.Cluster{
		AppName:     p.Repo.Name,
		Env:         p.Repo.Environment,
		Provider:    string(p.Config.ComputeProvider),
		Credentials: p.Env,
		SSHKey:      []byte(p.Repo.SSHPrivateKey),
	}

	// Provider refs from typed RepoConfig columns. Credentials from encrypted env.
	dnsRef := pkgcore.ProviderRef{Name: string(p.Config.DNSProvider), Creds: p.Env}
	storageRef := pkgcore.ProviderRef{Name: string(p.Config.StorageProvider), Creds: p.Env}
	buildProvider := string(p.Config.BuildProvider)

	// Load steps in order.
	var steps []api.DeploymentStep
	db.Where("deployment_id = ?", p.Deployment.ID).Order("position").Find(&steps)

	var lastErr error
	builtImages := map[string]string{}

	for i := range steps {
		step := &steps[i]
		out := newDBOutput(db, step.ID)
		markStepRunning(db, step)

		var params map[string]any
		if step.Params != "" {
			json.Unmarshal([]byte(step.Params), &params)
		}

		err := executeStep(ctx, cluster, dnsRef, storageRef, buildProvider, p.Env, config.StepKind(step.Kind), step.Name, params, out, builtImages)
		markStepDone(db, step, err)

		if err != nil {
			lastErr = err
			skipRemainingSteps(db, p.Deployment.ID)
			break
		}
	}

	markDeploymentDone(db, p.Deployment, lastErr)
}

// executeStep dispatches a single step to the corresponding pkg/core/ function.
func executeStep(ctx context.Context, cluster pkgcore.Cluster, dnsRef, storageRef pkgcore.ProviderRef, buildProvider string, creds map[string]string, kind config.StepKind, name string, params map[string]any, out pkgcore.Output, builtImages map[string]string) error {
	cluster.Output = out

	switch kind {
	case config.StepComputeSet:
		_, err := pkgcore.ComputeSet(ctx, pkgcore.ComputeSetRequest{
			Cluster:    cluster,
			Name:       name,
			ServerType: utils.GetString(params, "type"),
			Region:     utils.GetString(params, "region"),
			Worker:     utils.GetBool(params, "worker"),
		})
		return err

	case config.StepComputeDelete:
		return pkgcore.ComputeDelete(ctx, pkgcore.ComputeDeleteRequest{
			Cluster: cluster,
			Name:    name,
		})

	case config.StepVolumeSet:
		_, err := pkgcore.VolumeSet(ctx, pkgcore.VolumeSetRequest{
			Cluster: cluster,
			Name:    name,
			Size:    utils.GetInt(params, "size"),
			Server:  utils.GetString(params, "server"),
		})
		return err

	case config.StepVolumeDelete:
		return pkgcore.VolumeDelete(ctx, pkgcore.VolumeDeleteRequest{
			Cluster: cluster,
			Name:    name,
		})

	case config.StepBuild:
		result, err := pkgcore.BuildRun(ctx, pkgcore.BuildRunRequest{
			Cluster:            cluster,
			Builder:            buildProvider,
			BuilderCredentials: creds,
			Source:             utils.GetString(params, "source"),
			Name:               name,
			GitToken:           creds["GITHUB_TOKEN"],
		})
		if err != nil {
			return err
		}
		builtImages[name] = result.ImageRef
		return nil

	case config.StepSecretSet:
		return pkgcore.SecretSet(ctx, pkgcore.SecretSetRequest{
			Cluster: cluster,
			Key:     name,
			Value:   utils.GetString(params, "value"),
		})

	case config.StepSecretDelete:
		return pkgcore.SecretDelete(ctx, pkgcore.SecretDeleteRequest{
			Cluster: cluster,
			Key:     name,
		})

	case config.StepStorageSet:
		return pkgcore.StorageSet(ctx, pkgcore.StorageSetRequest{
			Cluster:    cluster,
			Storage:    storageRef,
			Name:       name,
			Bucket:     utils.GetString(params, "bucket"),
			CORS:       utils.GetBool(params, "cors"),
			ExpireDays: utils.GetInt(params, "expire_days"),
		})

	case config.StepStorageDelete:
		return pkgcore.StorageDelete(ctx, pkgcore.StorageDeleteRequest{
			Cluster: cluster,
			Storage: storageRef,
			Name:    name,
		})

	case config.StepServiceSet:
		image := utils.GetString(params, "image")
		if buildRef := utils.GetString(params, "build"); buildRef != "" {
			if ref, ok := builtImages[buildRef]; ok {
				image = ref
			} else {
				ref, err := pkgcore.BuildLatest(ctx, pkgcore.BuildLatestRequest{
					Cluster: cluster,
					Name:    buildRef,
				})
				if err != nil {
					return fmt.Errorf("resolve image for build %q: %w", buildRef, err)
				}
				image = ref
				builtImages[buildRef] = ref
			}
		}

		return pkgcore.ServiceSet(ctx, pkgcore.ServiceSetRequest{
			Cluster:    cluster,
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
			Cluster: cluster,
			Name:    name,
		})

	case config.StepDNSSet:
		return pkgcore.DNSSet(ctx, pkgcore.DNSSetRequest{
			Cluster: cluster,
			DNS:     dnsRef,
			Service: name,
			Domains: utils.GetStringSlice(params, "domains"),
		})

	case config.StepDNSDelete:
		return pkgcore.DNSDelete(ctx, pkgcore.DNSDeleteRequest{
			Cluster: cluster,
			DNS:     dnsRef,
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
