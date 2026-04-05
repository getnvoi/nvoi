package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push config + env to the active repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			configPath, _ := cmd.Flags().GetString("config")
			envPath, _ := cmd.Flags().GetString("env")
			computeProvider, _ := cmd.Flags().GetString("compute-provider")
			dnsProvider, _ := cmd.Flags().GetString("dns-provider")
			storageProvider, _ := cmd.Flags().GetString("storage-provider")
			buildProvider, _ := cmd.Flags().GetString("build-provider")

			configData, err := os.ReadFile(configPath)
			if err != nil {
				return fmt.Errorf("read config: %w", err)
			}

			var envData string
			if envPath != "" {
				b, err := os.ReadFile(envPath)
				if err != nil {
					return fmt.Errorf("read env: %w", err)
				}
				envData = string(b)
			}

			body := map[string]any{
				"compute_provider": computeProvider,
				"config":           string(configData),
			}
			if envData != "" {
				body["env"] = envData
			}
			if dnsProvider != "" {
				body["dns_provider"] = dnsProvider
			}
			if storageProvider != "" {
				body["storage_provider"] = storageProvider
			}
			if buildProvider != "" {
				body["build_provider"] = buildProvider
			}

			var resp struct {
				Version int `json:"version"`
			}
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/config"
			if err := client.Do("POST", path, body, &resp); err != nil {
				return err
			}

			fmt.Printf("pushed config v%d\n", resp.Version)
			return nil
		},
	}

	cmd.Flags().String("config", "nvoi.yaml", "path to config YAML")
	cmd.Flags().String("env", ".env", "path to .env file")
	cmd.Flags().String("compute-provider", "", "compute provider (required)")
	cmd.Flags().String("dns-provider", "", "DNS provider")
	cmd.Flags().String("storage-provider", "", "storage provider")
	cmd.Flags().String("build-provider", "", "build provider")

	return cmd
}
