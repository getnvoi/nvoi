package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewBuildCmd returns the build command group.
func NewBuildCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build images and push to cluster registry",
		Long: `Builds container images and pushes to the cluster registry.
--target is name:source. One target = single build. Multiple = parallel.

Examples:
  nvoi build --target web:./cmd/web
  nvoi build --target web:./cmd/web --target api:./cmd/api
  nvoi build --target web:benbonnet/dummy-rails --build-provider daytona`,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, _ := cmd.Flags().GetStringArray("target")
			if len(targets) == 0 {
				return fmt.Errorf("at least one --target name:source is required")
			}

			branch, _ := cmd.Flags().GetString("branch")
			platform, _ := cmd.Flags().GetString("platform")
			architecture, _ := cmd.Flags().GetString("architecture")
			history, _ := cmd.Flags().GetInt("history")

			return b.Build(cmd.Context(), BuildOpts{
				Targets:      targets,
				Branch:       branch,
				Platform:     platform,
				Architecture: architecture,
				History:      history,
			})
		},
	}
	cmd.Flags().StringArray("target", nil, "build target (name:source, repeatable)")
	cmd.Flags().String("branch", "main", "git branch (remote sources only)")
	cmd.Flags().String("platform", "", "target platform (auto-detected if empty)")
	cmd.Flags().String("architecture", "", "target architecture (amd64, arm64)")
	cmd.Flags().Int("history", 0, "keep N most recent tags, prune the rest (0 = keep all)")

	cmd.AddCommand(newBuildListCmd(b))
	cmd.AddCommand(newBuildLatestCmd(b))
	cmd.AddCommand(newBuildPruneCmd(b))

	return cmd
}

func newBuildListCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List images in the cluster registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.BuildList(cmd.Context())
		},
	}
}

func newBuildLatestCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "latest [name]",
		Short: "Return the latest image ref (pipeable)",
		Long: `Returns just the image reference string, for use in scripts:

  IMAGE=$(nvoi build latest web)
  nvoi service set web --image $IMAGE --port 80`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := b.BuildLatest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			// Raw output — no decoration. Used in scripts.
			fmt.Println(ref)
			return nil
		},
	}
}

func newBuildPruneCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune [name]",
		Short: "Keep N most recent tags, delete the rest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keep, _ := cmd.Flags().GetInt("keep")
			return b.BuildPrune(cmd.Context(), args[0], keep)
		},
	}
	cmd.Flags().Int("keep", 3, "number of recent tags to keep")
	return cmd
}
