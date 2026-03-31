package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [repo]",
		Short: "Build images via Daytona and push to cluster registry",
		Long: `Builds container images from git repos using Daytona remote builds.
Pushes to the cluster registry.

Without arguments: builds all services with a build field.
With a repo argument: builds that specific repo.

  nvoi build
  nvoi build myorg/myapp
  nvoi build myorg/myapp --branch feature-x`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var repo string
			if len(args) > 0 {
				repo = args[0]
			}
			branch, _ := cmd.Flags().GetString("branch")

			_ = repo
			_ = branch

			// TODO Phase 5:
			// 1. Resolve master from provider API → SSH
			// 2. Get registry addr (registry health check over SSH)
			// 3. Resolve builder (Daytona)
			// 4. If repo arg: build that repo
			//    If no arg: query cluster for services with build annotation, build each unique repo
			// 5. Push to cluster registry via SSH tunnel
			// 6. Update service image via kubectl set image (on remote)
			return fmt.Errorf("not implemented")
		},
	}
	cmd.Flags().String("branch", "main", "git branch to build")
	return cmd
}
