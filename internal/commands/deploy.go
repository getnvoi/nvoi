package commands

import (
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewDeployCmd(dc *reconcile.DeployContext) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy from config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return reconcile.Deploy(cmd.Context(), dc, cfg, viper.GetViper())
		},
	}
}

func loadConfig(cmd *cobra.Command) (*reconcile.AppConfig, error) {
	path, _ := cmd.Flags().GetString("config")
	if path == "" {
		path = "nvoi.yaml"
	}
	return reconcile.ParseAppConfig(path)
}
