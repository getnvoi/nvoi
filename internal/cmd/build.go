package cmd

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/app"
	_ "github.com/getnvoi/nvoi/internal/provider/daytona" // register daytona builder
	_ "github.com/getnvoi/nvoi/internal/provider/local"   // register local builder
	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build images and push to cluster registry",
		Long: `Builds a container image and pushes to the cluster registry.

Examples:
  nvoi build --provider hetzner --builder local --source . --name web
  nvoi build --provider hetzner --builder daytona --source benbonnet/dummy-rails --name web`,
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")
			builderName, _ := cmd.Flags().GetString("builder")
			source, _ := cmd.Flags().GetString("source")
			name, _ := cmd.Flags().GetString("name")
			branch, _ := cmd.Flags().GetString("branch")
			platform, _ := cmd.Flags().GetString("platform")

			appName, env, err := resolveAppEnv()
			if err != nil {
				return err
			}
			creds, err := resolveCredentials(cmd, providerName)
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

			_, err = app.BuildRun(cmd.Context(), app.BuildRunRequest{
				AppName:            appName,
				Env:                env,
				Provider:           providerName,
				Credentials:        creds,
				Builder:            builderName,
				BuilderCredentials: builderCreds,
				SSHKey:             sshKey,
				Source:             source,
				Name:               name,
				Branch:             branch,
				Platform:           platform,
				GitUsername:         gitUsername,
				GitToken:           gitToken,
			})
			return err
		},
	}
	addProviderFlags(cmd)
	cmd.Flags().String("builder", "local", "build provider (local, daytona)")
	cmd.Flags().StringArray("builder-credentials", nil, "build provider credentials (key=value)")
	cmd.Flags().String("source", "", "source to build (local path or remote repo)")
	cmd.Flags().String("name", "", "image name in registry")
	cmd.Flags().String("branch", "main", "git branch (remote sources only)")
	cmd.Flags().String("platform", "", "target platform (auto-detected if empty)")
	cmd.Flags().String("git-token", "", "git token for private repo cloning")
	_ = cmd.MarkFlagRequired("source")
	_ = cmd.MarkFlagRequired("name")

	cmd.AddCommand(newBuildListCmd())
	cmd.AddCommand(newBuildLatestCmd())

	return cmd
}

func newBuildListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List images in the cluster registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")

			appName, env, err := resolveAppEnv()
			if err != nil {
				return err
			}
			creds, err := resolveCredentials(cmd, providerName)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			images, err := app.BuildList(cmd.Context(), app.BuildListRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				SSHKey:      sshKey,
			})
			if err != nil {
				return err
			}

			if len(images) == 0 {
				fmt.Println("no images in registry")
				return nil
			}

			t := NewTable("IMAGE", "TAGS")
			for _, img := range images {
				t.Row(img.Name, strings.Join(img.Tags, ", "))
			}
			t.Print()
			return nil
		},
	}
	addProviderFlags(cmd)
	return cmd
}

func newBuildLatestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "latest [name]",
		Short: "Return the latest image ref (pipeable)",
		Long: `Returns just the image reference string, for use in scripts:

  IMAGE=$(nvoi build latest web --provider hetzner)
  nvoi service set web --provider hetzner --image $IMAGE --port 80`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")

			appName, env, err := resolveAppEnv()
			if err != nil {
				return err
			}
			creds, err := resolveCredentials(cmd, providerName)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			ref, err := app.BuildLatest(cmd.Context(), app.BuildLatestRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				SSHKey:      sshKey,
				Name:        args[0],
			})
			if err != nil {
				return err
			}

			// Raw output — no decoration. Used in scripts: IMAGE=$(nvoi build latest web ...)
			fmt.Println(ref)
			return nil
		},
	}
	addProviderFlags(cmd)
	return cmd
}
