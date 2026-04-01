package cmd

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/app"
	_ "github.com/getnvoi/nvoi/internal/provider/hetzner" // register
	"github.com/spf13/cobra"
)

func newComputeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compute",
		Short: "Manage compute servers",
	}
	cmd.AddCommand(newComputeSetCmd())
	cmd.AddCommand(newComputeDeleteCmd())
	cmd.AddCommand(newComputeListCmd())
	return cmd
}

func newComputeSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Provision a server and install k3s (master by default, --worker to join)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")
			serverType, _ := cmd.Flags().GetString("type")
			region, _ := cmd.Flags().GetString("region")
			worker, _ := cmd.Flags().GetBool("worker")

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

			_, err = app.ComputeSet(cmd.Context(), app.ComputeSetRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				SSHKey:      sshKey,
				Name:        args[0],
				ServerType:  serverType,
				Region:      region,
				Worker:      worker,
			})
			return err
		},
	}
	addProviderFlags(cmd)
	cmd.Flags().String("type", "", "server instance type (e.g. cax11)")
	cmd.Flags().String("region", "", "server region (e.g. fsn1)")
	cmd.Flags().Bool("worker", false, "join as worker (default: master)")
	_ = cmd.MarkFlagRequired("type")
	_ = cmd.MarkFlagRequired("region")
	return cmd
}

func newComputeDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a server (firewall + network cleaned up)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Delete server %s? [y/N] ", args[0])
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

			return app.ComputeDelete(cmd.Context(), app.ComputeDeleteRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				Name:        args[0],
			})
		},
	}
	addProviderFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}

func newComputeListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List provisioned servers",
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

			servers, err := app.ComputeList(cmd.Context(), app.ComputeListRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
			})
			if err != nil {
				return err
			}

			t := NewTable("NAME", "STATUS", "IPv4", "PRIVATE IP")
			for _, s := range servers {
				t.Row(s.Name, s.Status, s.IPv4, s.PrivateIP)
			}
			t.Print()
			return nil
		},
	}
	addProviderFlags(cmd)
	return cmd
}
