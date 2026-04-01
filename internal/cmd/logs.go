package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [service]",
		Short: "Show service logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			follow, _ := cmd.Flags().GetBool("follow")
			tail, _ := cmd.Flags().GetInt("tail")

			_, _, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			_, err = resolveComputeProvider(cmd)
			if err != nil {
				return err
			}

			_ = service
			_ = follow
			_ = tail

			// TODO Phase 2:
			// 1. Resolve master from provider API → SSH
			// 2. kubectl logs deployment/{service} --tail={n} (over SSH)
			// 3. If --follow: stream over SSH
			return fmt.Errorf("not implemented")
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	cmd.Flags().IntP("tail", "n", 50, "number of lines to show")
	return cmd
}
