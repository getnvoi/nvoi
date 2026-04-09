// Package commands defines the shared command tree used by both the direct CLI and the cloud CLI.
package commands

import "github.com/spf13/cobra"

// NewAgentCmd returns the agent command group.
func NewAgentCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agents",
	}
	cmd.AddCommand(newAgentSetCmd(b))
	cmd.AddCommand(newAgentDeleteCmd(b))
	cmd.AddCommand(newAgentListCmd(b))
	cmd.AddCommand(newAgentExecCmd(b))
	cmd.AddCommand(newAgentLogsCmd(b))
	return cmd
}

func newAgentSetCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Deploy a managed agent to the cluster",
		Long: `Compiles an agent bundle and executes all owned primitive operations.

Required credentials are read from the cluster via --secret.

Examples:
  nvoi agent set coder --type claude --secret NVOI_AGENT_TOKEN`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveManagedKind(cmd, "agent")
			if err != nil {
				return err
			}
			secrets, _ := cmd.Flags().GetStringArray("secret")

			return b.AgentSet(cmd.Context(), args[0], ManagedOpts{
				Kind:    kind,
				Secrets: secrets,
			})
		},
	}
	cmd.Flags().String("type", "", "agent type (claude)")
	cmd.Flags().StringArray("secret", nil, "secret key to read from cluster")
	return cmd
}

func newAgentDeleteCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a managed agent and all owned resources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveManagedKind(cmd, "agent")
			if err != nil {
				return err
			}
			return b.AgentDelete(cmd.Context(), args[0], kind)
		},
	}
	cmd.Flags().String("type", "", "agent type (claude)")
	return cmd
}

func newAgentListCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.AgentList(cmd.Context())
		},
	}
}

func newAgentExecCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [name] -- [command...]",
		Short: "Run a command in an agent pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveManagedKind(cmd, "agent")
			if err != nil {
				return err
			}
			return b.AgentExec(cmd.Context(), args[0], kind, args[1:])
		},
	}
	cmd.Flags().String("type", "", "agent type (claude)")
	return cmd
}

func newAgentLogsCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [name]",
		Short: "Show agent logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveManagedKind(cmd, "agent")
			if err != nil {
				return err
			}
			follow, _ := cmd.Flags().GetBool("follow")
			tail, _ := cmd.Flags().GetInt("tail")
			since, _ := cmd.Flags().GetString("since")
			previous, _ := cmd.Flags().GetBool("previous")
			timestamps, _ := cmd.Flags().GetBool("timestamps")

			return b.AgentLogs(cmd.Context(), args[0], kind, LogsOpts{
				Follow:     follow,
				Tail:       tail,
				Since:      since,
				Previous:   previous,
				Timestamps: timestamps,
			})
		},
	}
	cmd.Flags().String("type", "", "agent type (claude)")
	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	cmd.Flags().IntP("tail", "n", 50, "number of lines to show")
	cmd.Flags().String("since", "", "show logs since duration (5m, 1h)")
	cmd.Flags().Bool("previous", false, "show previous container logs")
	cmd.Flags().Bool("timestamps", false, "show timestamps")
	return cmd
}
