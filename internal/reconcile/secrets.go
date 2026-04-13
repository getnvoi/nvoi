package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/viper"
)

func Secrets(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig, v *viper.Viper) error {
	for _, key := range cfg.Secrets {
		val := v.GetString(key)
		if val == "" {
			return fmt.Errorf("secret %q listed in config but not found in environment", key)
		}
		if err := app.SecretSet(ctx, app.SecretSetRequest{Cluster: dc.Cluster, Key: key, Value: val}); err != nil {
			return err
		}
	}
	if live != nil {
		desired := toSet(cfg.Secrets)
		for _, key := range live.Secrets {
			if !desired[key] {
				if err := app.SecretDelete(ctx, app.SecretDeleteRequest{Cluster: dc.Cluster, Key: key}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan secret %s not removed: %s", key, err))
				}
			}
		}
	}
	return nil
}
