package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/packages"
)

// ErrDeployInterrupted is returned when a deploy is cancelled mid-reconcile.
// The error message includes the last completed step so the user knows where
// it stopped. The next `nvoi deploy` picks up from there — idempotent by design.
var ErrDeployInterrupted = fmt.Errorf("deploy interrupted")

// checkCancel returns ErrDeployInterrupted if the context has been cancelled,
// annotated with the last completed step.
func checkCancel(ctx context.Context, lastStep string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w after %s: %v", ErrDeployInterrupted, lastStep, ctx.Err())
	}
	return nil
}

// Deploy reconciles live infrastructure to match the YAML config.
func Deploy(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if err := cfg.Resolve(); err != nil {
		return err
	}

	live, err := DescribeLive(ctx, dc, cfg)
	if err != nil {
		return err
	}

	// ServersAdd: agent provisions workers. Bootstrap doesn't call Deploy() —
	// it provisions the master directly, installs the agent, and delegates.
	if err := ServersAdd(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := checkCancel(ctx, "servers"); err != nil {
		return err
	}

	if err := Firewall(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := checkCancel(ctx, "firewall"); err != nil {
		return err
	}
	if err := Volumes(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := checkCancel(ctx, "volumes"); err != nil {
		return err
	}
	if err := Build(ctx, dc, cfg); err != nil {
		return err
	}
	if err := checkCancel(ctx, "build"); err != nil {
		return err
	}
	secretValues, err := Secrets(ctx, dc, live, cfg)
	if err != nil {
		return err
	}
	if err := checkCancel(ctx, "secrets"); err != nil {
		return err
	}

	// Packages (database, etc.) — after volumes/secrets, before services.
	// Returns env vars available as $VAR resolution sources.
	packageEnvVars, err := packages.ReconcileAll(ctx, dc, cfg)
	if err != nil {
		return err
	}
	if err := checkCancel(ctx, "packages"); err != nil {
		return err
	}

	storageCreds, err := Storage(ctx, dc, live, cfg)
	if err != nil {
		return err
	}
	if err := checkCancel(ctx, "storage"); err != nil {
		return err
	}

	// Build unified sources for $VAR resolution and per-service secret storage.
	sources := mergeSources(secretValues, packageEnvVars, storageCreds)

	if err := Services(ctx, dc, live, cfg, sources); err != nil {
		return err
	}
	if err := checkCancel(ctx, "services"); err != nil {
		return err
	}
	if err := Crons(ctx, dc, live, cfg, sources); err != nil {
		return err
	}
	if err := checkCancel(ctx, "crons"); err != nil {
		return err
	}

	// Workloads have moved. Now safe to drain + delete orphan servers.
	if err := ServersRemoveOrphans(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := checkCancel(ctx, "orphan removal"); err != nil {
		return err
	}

	if err := DNS(ctx, dc, live, cfg); err != nil {
		return err
	}

	// Verify DNS propagation before ACME — warn if domains don't resolve yet.
	if len(cfg.Domains) > 0 {
		verifyDNSPropagation(ctx, dc, cfg)
	}

	return Ingress(ctx, dc, live, cfg)
}
