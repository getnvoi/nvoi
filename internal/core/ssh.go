package core

import (
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newSSHCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh [command...]",
		Short: "Run a command on the host server",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			providerName, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			creds, err := resolveComputeCredentials(cmd, providerName)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			return app.SSH(cmd.Context(), app.SSHRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Command: args,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}
