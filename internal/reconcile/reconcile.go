package reconcile

import (
	"context"

	"github.com/spf13/viper"
)

// Deploy reconciles live infrastructure to match the YAML config.
func Deploy(ctx context.Context, dc *DeployContext, cfg *AppConfig, v *viper.Viper) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}

	live := DescribeLive(ctx, dc)

	if err := Servers(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Firewall(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Volumes(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Build(ctx, dc, cfg); err != nil {
		return err
	}
	if err := Secrets(ctx, dc, live, cfg, v); err != nil {
		return err
	}
	if err := Storage(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Services(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Crons(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := DNS(ctx, dc, live, cfg); err != nil {
		return err
	}
	return Ingress(ctx, dc, live, cfg)
}
