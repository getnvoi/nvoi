package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/getnvoi/nvoi/internal/cli"
	"github.com/spf13/cobra"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := rootCmd().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
	}

	// All commands need auth — PersistentPreRunE loads client.
	// Standalone commands (login, whoami, etc.) override it.
	var client *cli.APIClient
	var wsID, repoID string

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		c, cfg, err := cli.AuthedClient()
		if err != nil {
			return err
		}
		ws, repo, err := cli.RequireRepo(cfg)
		if err != nil {
			return err
		}
		client = c
		wsID = ws
		repoID = repo
		return nil
	}

	repoPath := func(suffix string) string {
		return "/workspaces/" + wsID + "/repos/" + repoID + suffix
	}

	root.AddCommand(cli.NewDeployCmd(&client, &repoPath))
	root.AddCommand(cli.NewTeardownCmd(&client, &repoPath))
	root.AddCommand(cli.NewDescribeCmd(&client, &repoPath))
	root.AddCommand(cli.NewResourcesCmd(&client, &repoPath))
	root.AddCommand(cli.NewLogsCmd(&client, &repoPath))
	root.AddCommand(cli.NewExecCmd(&client, &repoPath))
	root.AddCommand(cli.NewSSHCmd(&client, &repoPath))
	root.AddCommand(cli.NewCronCmd(&client, &repoPath))

	addStandalone(root, cli.NewLoginCmd())
	addStandalone(root, cli.NewWhoamiCmd())
	addStandalone(root, cli.NewWorkspacesCmd())
	addStandalone(root, cli.NewReposCmd())
	addStandalone(root, cli.NewProviderCmd())

	return root
}

func addStandalone(root *cobra.Command, cmd *cobra.Command) {
	cmd.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
	root.AddCommand(cmd)
}
