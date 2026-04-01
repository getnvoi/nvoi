package cmd

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/app"
	"github.com/spf13/cobra"
)

func newVolumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Manage block storage volumes",
	}
	cmd.AddCommand(newVolumeSetCmd())
	cmd.AddCommand(newVolumeDeleteCmd())
	cmd.AddCommand(newVolumeListCmd())
	return cmd
}

func newVolumeSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Provision or reconcile a block storage volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")
			size, _ := cmd.Flags().GetInt("size")
			server, _ := cmd.Flags().GetString("server")

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

			_, err = app.VolumeSet(cmd.Context(), app.VolumeSetRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				SSHKey:      sshKey,
				Name:        args[0],
				Size:        size,
				Server:      server,
			})
			return err
		},
	}
	addProviderFlags(cmd)
	cmd.Flags().Int("size", 10, "volume size in GB")
	cmd.Flags().String("server", "master", "target server name")
	_ = cmd.MarkFlagRequired("size")
	return cmd
}

func newVolumeDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Detach a volume (data preserved)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Detach volume %s? Data preserved. [y/N] ", args[0])
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

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

			return app.VolumeDelete(cmd.Context(), app.VolumeDeleteRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				SSHKey:      sshKey,
				Name:        args[0],
			})
		},
	}
	addProviderFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}

func newVolumeListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List volumes",
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

			volumes, err := app.VolumeList(cmd.Context(), app.VolumeListRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
			})
			if err != nil {
				return err
			}

			t := NewTable("NAME", "SIZE", "SERVER", "DEVICE")
			for _, v := range volumes {
				t.Row(v.Name, fmt.Sprintf("%dGB", v.Size), v.ServerName, v.DevicePath)
			}
			t.Print()
			return nil
		},
	}
	addProviderFlags(cmd)
	return cmd
}
