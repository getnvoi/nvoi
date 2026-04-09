package reconcile

import (
	"context"
	"fmt"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/viper"
)

func Secrets(ctx context.Context, dc *DeployContext, live *LiveState, cfg *AppConfig, v *viper.Viper) error {
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
				_ = app.SecretDelete(ctx, app.SecretDeleteRequest{Cluster: dc.Cluster, Key: key})
			}
		}
	}
	return nil
}
