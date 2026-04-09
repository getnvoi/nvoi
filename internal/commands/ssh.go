package commands

import "github.com/spf13/cobra"

// NewSSHCmd returns the ssh command.
func NewSSHCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh [command...]",
		Short: "Run a command on the host server",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.SSH(cmd.Context(), args)
		},
	}
}
