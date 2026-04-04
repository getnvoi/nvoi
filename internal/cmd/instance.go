package cmd

import (
	"fmt"

	"github.com/getnvoi/nvoi/pkg/app"
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"      // register
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"  // register
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway" // register
	"github.com/spf13/cobra"
)

func newInstanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instance",
		Short: "Manage compute instances",
	}
	cmd.AddCommand(newInstanceSetCmd())
	cmd.AddCommand(newInstanceDeleteCmd())
	cmd.AddCommand(newInstanceListCmd())
	return cmd
}

func newInstanceSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Provision an instance and install k3s (master by default, --worker to join)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			computeType, _ := cmd.Flags().GetString("compute-type")
			region, _ := cmd.Flags().GetString("compute-region")
			worker, _ := cmd.Flags().GetBool("worker")

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
			// --compute-region overrides the region in credentials (e.g. AWS_REGION).
			// The provider SDK client is initialized from creds — this ensures
			// the flag wins over the env var.
			if region != "" {
				creds["region"] = region
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			_, err = app.ComputeSet(cmd.Context(), app.ComputeSetRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Name:       args[0],
				ServerType: computeType,
				Region:     region,
				Worker:     worker,
			})
			return err
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("compute-type", "", "instance type (e.g. cax11)")
	cmd.Flags().String("compute-region", "", "instance region (e.g. fsn1)")
	cmd.Flags().Bool("worker", false, "join as worker (default: master)")
	_ = cmd.MarkFlagRequired("compute-type")
	_ = cmd.MarkFlagRequired("compute-region")
	return cmd
}

func newInstanceDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete an instance (firewall + network cleaned up)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Delete instance %s? [y/N] ", args[0])
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

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

			return app.ComputeDelete(cmd.Context(), app.ComputeDeleteRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					Output:      resolveOutput(cmd),
				},
				Name: args[0],
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}

func newInstanceListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List provisioned instances",
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

			servers, err := app.ComputeList(cmd.Context(), app.ComputeListRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					Output:      resolveOutput(cmd),
				},
			})
			if err != nil {
				return err
			}

			t := NewTable("NAME", "STATUS", "IPv4", "PRIVATE IP")
			for _, s := range servers {
				t.Row(s.Name, string(s.Status), s.IPv4, s.PrivateIP)
			}
			t.Print()
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}
