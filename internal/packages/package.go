// Package packages defines the interface and registry for higher-level
// abstractions that bundle infrastructure, secrets, and CLI commands.
package packages

import (
	"context"

	"github.com/getnvoi/nvoi/internal/config"
)

// Package is a higher-level abstraction that bundles infra + secrets + CLI.
type Package interface {
	Name() string
	Validate(cfg *config.AppConfig) error
	Reconcile(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) (envVars map[string]string, err error)
	Teardown(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, deleteStorage bool) error
	Active(cfg *config.AppConfig) bool
}

var registry []Package

func Register(p Package) {
	registry = append(registry, p)
}

func ValidateAll(cfg *config.AppConfig) error {
	for _, p := range registry {
		if p.Active(cfg) {
			if err := p.Validate(cfg); err != nil {
				return err
			}
		}
	}
	return nil
}

func ReconcileAll(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) (map[string]string, error) {
	merged := map[string]string{}
	for _, p := range registry {
		if p.Active(cfg) {
			envVars, err := p.Reconcile(ctx, dc, cfg)
			if err != nil {
				return nil, err
			}
			for k, v := range envVars {
				merged[k] = v
			}
		}
	}
	return merged, nil
}

func TeardownAll(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, deleteStorage bool) {
	for _, p := range registry {
		if p.Active(cfg) {
			_ = p.Teardown(ctx, dc, cfg, deleteStorage)
		}
	}
}
