package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/getnvoi/nvoi/internal/app"
	"github.com/spf13/cobra"
)

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "nvoi",
		Short:         "Deploy containers to cloud servers",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Persistent flags.
	root.PersistentFlags().String("env-file", ".env", "path to .env file")
	root.PersistentFlags().Bool("json", false, "output JSONL")

	// Infrastructure.
	root.AddCommand(newInstanceCmd())
	root.AddCommand(newVolumeCmd())
	root.AddCommand(newDNSCmd())
	root.AddCommand(newStorageCmd())

	// Application.
	root.AddCommand(newServiceCmd())
	root.AddCommand(newSecretCmd())

	// Build.
	root.AddCommand(newBuildCmd())

	// Live view.
	root.AddCommand(newDescribeCmd())

	// Operate.
	root.AddCommand(newLogsCmd())
	root.AddCommand(newExecCmd())
	root.AddCommand(newSSHCmd())

	// Inspect.
	root.AddCommand(newResourcesCmd())

	// Teardown — use bin/destroy (the script). No CLI command needed.

	// Style errors through Output.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		resolveOutput(cmd).Error(err)
		return err
	})

	return root
}

func envFilePath(cmd *cobra.Command) string {
	p, _ := cmd.Flags().GetString("env-file")
	return p
}

func resolveOutput(cmd *cobra.Command) app.Output {
	j, _ := cmd.Flags().GetBool("json")
	if j {
		return NewJSONOutput(os.Stdout)
	}
	return NewTUIOutput()
}

// HandleError styles an error through the Output system.
// Called from main after Execute returns an error.
func HandleError(ctx context.Context, cmd *cobra.Command, err error) {
	if err == nil {
		return
	}
	if ctx.Err() != nil {
		// Signal interrupt — clean exit message
		fmt.Fprintln(os.Stderr)
		resolveOutput(cmd).Error(fmt.Errorf("interrupted"))
		return
	}
	resolveOutput(cmd).Error(err)
}
