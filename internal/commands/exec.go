package commands

import "github.com/spf13/cobra"

// NewExecCmd returns the exec command.
func NewExecCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "exec [service] -- [command...]",
		Short: "Run a command in a service pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.Exec(cmd.Context(), args[0], args[1:])
		},
	}
}
