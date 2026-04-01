package cmd

import (
	"github.com/getnvoi/nvoi/internal/app"
	"github.com/spf13/cobra"
)

func newSSHCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh [command...]",
		Short: "Run a command on the host server",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")

			appName, env, err := resolveAppEnv()
			if err != nil {
				return err
			}
			creds, err := resolveCredentials(cmd, providerName)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			return app.SSH(cmd.Context(), app.SSHRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				SSHKey:      sshKey,
				Command:     args,
			})
		},
	}
	addProviderFlags(cmd)
	return cmd
}
