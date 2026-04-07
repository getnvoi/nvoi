package core

import (
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newIngressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingress",
		Short: "Manage Caddy ingress",
	}
	cmd.AddCommand(newIngressApplyCmd())
	return cmd
}

func newIngressApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply [service:domain,domain ...]",
		Short: "Deploy Caddy with all specified routes in a single rollout",
		Long: `Builds the Caddyfile from service:domain mappings and deploys Caddy once.

Examples:
  nvoi ingress apply web:example.com,www.example.com api:api.example.com
  nvoi ingress apply web:myapp.com`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			computeProvider, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			computeCreds, err := resolveComputeCredentials(cmd, computeProvider)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			routes, err := app.ParseIngressArgs(args)
			if err != nil {
				return err
			}

			return app.IngressApply(cmd.Context(), app.IngressApplyRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Routes: routes,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}
