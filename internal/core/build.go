package core

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	_ "github.com/getnvoi/nvoi/pkg/provider/daytona" // register daytona builder
	_ "github.com/getnvoi/nvoi/pkg/provider/github"  // register github actions builder
	_ "github.com/getnvoi/nvoi/pkg/provider/local"   // register local builder
	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
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

			parsed, err := app.ParseBuildTargets(targets)
			if err != nil {
				return err
			}

			platform, _ := cmd.Flags().GetString("platform")
			architecture, _ := cmd.Flags().GetString("architecture")
			if architecture != "" {
				switch architecture {
				case "amd64", "amd":
					platform = "linux/amd64"
				case "arm64", "arm":
					platform = "linux/arm64"
				default:
					return fmt.Errorf("invalid architecture %q — use amd64 or arm64", architecture)
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
			builderName, err := resolveBuildProvider(cmd)
			if err != nil {
				return err
			}
			builderCreds, err := resolveBuildCredentials(cmd, builderName)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			gitUsername, gitToken := resolveGitAuth(cmd)

			// Single target → sequential BuildRun (supports --branch, --history)
			if len(parsed) == 1 {
				branch, _ := cmd.Flags().GetString("branch")
				history, _ := cmd.Flags().GetInt("history")

				_, err = app.BuildRun(cmd.Context(), app.BuildRunRequest{
					Cluster: app.Cluster{
						AppName:     appName,
						Env:         env,
						Provider:    providerName,
						Credentials: creds,
						SSHKey:      sshKey,
						Output:      resolveOutput(cmd),
					},
					Builder:            builderName,
					BuilderCredentials: builderCreds,
					Source:             parsed[0].Source,
					Name:               parsed[0].Name,
					Branch:             branch,
					Platform:           platform,
					GitUsername:        gitUsername,
					GitToken:           gitToken,
					History:            history,
				})
				return err
			}

			// Multiple targets → parallel
			_, err = app.BuildParallel(cmd.Context(), app.BuildParallelRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Builder:            builderName,
				BuilderCredentials: builderCreds,
				Targets:            parsed,
				Platform:           platform,
				GitUsername:        gitUsername,
				GitToken:           gitToken,
			})
			return err
		},
	}
	addComputeProviderFlags(cmd)
	addBuildProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().StringArray("target", nil, "build target (name:source, repeatable)")
	cmd.Flags().String("branch", "main", "git branch (remote sources only)")
	cmd.Flags().String("platform", "", "target platform (auto-detected if empty)")
	cmd.Flags().String("architecture", "", "target architecture (amd64, arm64)")
	cmd.Flags().String("git-token", "", "git token for private repo cloning")
	cmd.Flags().Int("history", 0, "keep N most recent tags, prune the rest (0 = keep all)")

	cmd.AddCommand(newBuildListCmd())
	cmd.AddCommand(newBuildLatestCmd())
	cmd.AddCommand(newBuildPruneCmd())

	return cmd
}

func newBuildListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List images in the cluster registry",
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
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			images, err := app.BuildList(cmd.Context(), app.BuildListRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
			})
			if err != nil {
				return err
			}

			if len(images) == 0 {
				fmt.Println("no images in registry")
				return nil
			}

			t := render.NewTable("IMAGE", "TAGS")
			for _, img := range images {
				t.Row(img.Name, strings.Join(img.Tags, ", "))
			}
			t.Print()
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}

func newBuildLatestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "latest [name]",
		Short: "Return the latest image ref (pipeable)",
		Long: `Returns just the image reference string, for use in scripts:

  IMAGE=$(nvoi build latest web)
  nvoi service set web --image $IMAGE --port 80`,
		Args: cobra.ExactArgs(1),
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
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			ref, err := app.BuildLatest(cmd.Context(), app.BuildLatestRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Name: args[0],
			})
			if err != nil {
				return err
			}

			// Raw output — no decoration. Used in scripts: IMAGE=$(nvoi build latest web ...)
			fmt.Println(ref)
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}

func newBuildPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune [name]",
		Short: "Keep N most recent tags, delete the rest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keep, _ := cmd.Flags().GetInt("keep")

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
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			return app.BuildPrune(cmd.Context(), app.BuildPruneRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Name: args[0],
				Keep: keep,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().Int("keep", 3, "number of recent tags to keep")
	return cmd
}
