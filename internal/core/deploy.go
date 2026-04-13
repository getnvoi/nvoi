package core

import (
	"fmt"
	"os"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewDeployCmd(dc *config.DeployContext) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy from config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd)
			if err != nil {
				return err
			}
			return reconcile.Deploy(cmd.Context(), dc, cfg, viper.GetViper())
		},
	}
}

func LoadConfig(cmd *cobra.Command) (*config.AppConfig, error) {
	path, _ := cmd.Flags().GetString("config")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return config.ParseAppConfig(data)
}
