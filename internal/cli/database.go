package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newDatabaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "database",
		Short: "Manage databases",
	}
	cmd.AddCommand(newCloudDatabaseListCmd())
	cmd.AddCommand(newCloudDatabaseBackupCmd())
	return cmd
}

func newCloudDatabaseListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			var services []pkgcore.ManagedService
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/database"
			if err := client.Do("GET", path, nil, &services); err != nil {
				return err
			}

			if len(services) == 0 {
				fmt.Println("no managed databases found")
				return nil
			}
			for _, svc := range services {
				children := strings.Join(svc.Children, ", ")
				fmt.Printf("%s  type=%s  %s  %s  children=[%s]\n", svc.Name, svc.ManagedKind, svc.Image, svc.Ready, children)
			}
			return nil
		},
	}
}

func newCloudDatabaseBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage database backups",
	}
	cmd.AddCommand(newCloudBackupCreateCmd())
	cmd.AddCommand(newCloudBackupListCmd())
	cmd.AddCommand(newCloudBackupDownloadCmd())
	return cmd
}

func newCloudBackupCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create [name]",
		Short: "Create a backup now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			var resp struct{ Status string }
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/database/" + args[0] + "/backup/create"
			if err := client.Do("POST", path, nil, &resp); err != nil {
				return err
			}
			fmt.Printf("backup %s\n", resp.Status)
			return nil
		},
	}
}

func newCloudBackupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [name]",
		Short: "List backup artifacts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			var artifacts []pkgcore.BackupArtifact
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/database/" + args[0] + "/backup"
			if err := client.Do("GET", path, nil, &artifacts); err != nil {
				return err
			}

			if len(artifacts) == 0 {
				fmt.Println("no backups found")
				return nil
			}
			for _, a := range artifacts {
				fmt.Printf("%s  %d bytes  %s\n", a.Key, a.Size, a.LastModified)
			}
			return nil
		},
	}
}

func newCloudBackupDownloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "download [name] [key]",
		Short: "Download a backup artifact",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			path := "/workspaces/" + wsID + "/repos/" + repoID + "/database/" + args[0] + "/backup/" + args[1]
			resp, err := client.doRaw("GET", path)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			_, err = io.Copy(os.Stdout, resp.Body)
			return err
		},
	}
}
