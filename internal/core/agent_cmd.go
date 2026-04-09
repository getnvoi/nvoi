package core

import (
	"fmt"
	"strings"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/managed"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agents",
	}
	cmd.AddCommand(newAgentSetCmd())
	cmd.AddCommand(newAgentDeleteCmd())
	cmd.AddCommand(newAgentListCmd())
	cmd.AddCommand(newAgentExecCmd())
	cmd.AddCommand(newAgentLogsCmd())
	return cmd
}

func resolveAgentType(cmd *cobra.Command) (string, error) {
	kind, _ := cmd.Flags().GetString("type")
	if kind == "" {
		available := managed.KindsForCategory("agent")
		return "", fmt.Errorf("--type is required. Available agent types: %s", strings.Join(available, ", "))
	}
	return kind, nil
}

func newAgentSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Deploy a managed agent to the cluster",
		Long: `Compiles an agent bundle and executes all owned primitive operations.

Required credentials are read from the cluster via --secret.

Examples:
  nvoi agent set coder --type claude --secret NVOI_AGENT_TOKEN`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveAgentType(cmd)
			if err != nil {
				return err
			}
			secrets, _ := cmd.Flags().GetStringArray("secret")

			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			env, err := readSecretsFromCluster(cmd, cluster, secrets)
			if err != nil {
				return err
			}

			result, err := managed.Compile(managed.Request{
				Kind:    kind,
				Name:    args[0],
				Env:     env,
				Context: managed.Context{DefaultVolumeServer: "master"},
			})
			if err != nil {
				return err
			}

			for _, op := range result.Bundle.Operations {
				if err := execOperation(cmd.Context(), cluster, op); err != nil {
					return err
				}
			}
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("type", "", "agent type (claude)")
	cmd.Flags().StringArray("secret", nil, "secret key to read from cluster")
	return cmd
}

func newAgentDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a managed agent and all owned resources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveAgentType(cmd)
			if err != nil {
				return err
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				fmt.Printf("Delete agent %s and all owned resources? [y/N] ", args[0])
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			shape, err := managed.Shape(kind, args[0])
			if err != nil {
				return err
			}

			return deleteByShape(cmd, cluster, shape)
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("type", "", "agent type (claude)")
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}

func newAgentListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List managed agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			// List all agent-category kinds.
			kinds := managed.KindsForCategory("agent")
			var all []app.ManagedService
			for _, kind := range kinds {
				services, err := app.ManagedList(cmd.Context(), app.ManagedListRequest{
					Cluster: cluster,
					Kind:    kind,
				})
				if err != nil {
					return err
				}
				all = append(all, services...)
			}

			if len(all) == 0 {
				cluster.Output.Info("no managed agents found")
				return nil
			}
			for _, svc := range all {
				children := strings.Join(svc.Children, ", ")
				cluster.Output.Success(fmt.Sprintf("%s  type=%s  %s  %s  children=[%s]", svc.Name, svc.ManagedKind, svc.Image, svc.Ready, children))
			}
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}

func newAgentExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [name] -- [command...]",
		Short: "Run a command in an agent pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveAgentType(cmd)
			if err != nil {
				return err
			}

			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			if err := verifyManagedKind(cmd, cluster, args[0], kind); err != nil {
				return err
			}

			return app.Exec(cmd.Context(), app.ExecRequest{
				Cluster: cluster,
				Service: args[0],
				Command: args[1:],
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("type", "", "agent type (claude)")
	return cmd
}

func newAgentLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [name]",
		Short: "Show agent logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveAgentType(cmd)
			if err != nil {
				return err
			}

			follow, _ := cmd.Flags().GetBool("follow")
			tail, _ := cmd.Flags().GetInt("tail")
			since, _ := cmd.Flags().GetString("since")
			previous, _ := cmd.Flags().GetBool("previous")
			timestamps, _ := cmd.Flags().GetBool("timestamps")

			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			if err := verifyManagedKind(cmd, cluster, args[0], kind); err != nil {
				return err
			}

			return app.Logs(cmd.Context(), app.LogsRequest{
				Cluster:    cluster,
				Service:    args[0],
				Follow:     follow,
				Tail:       tail,
				Since:      since,
				Previous:   previous,
				Timestamps: timestamps,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("type", "", "agent type (claude)")
	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	cmd.Flags().IntP("tail", "n", 50, "number of lines to show")
	cmd.Flags().String("since", "", "show logs since duration (5m, 1h)")
	cmd.Flags().Bool("previous", false, "show previous container logs")
	cmd.Flags().Bool("timestamps", false, "show timestamps")
	return cmd
}
