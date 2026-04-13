package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

// configClient returns an authenticated API client and the repo-scoped config path.
func configClient() (*APIClient, string, error) {
	c, cfg, err := AuthedClient()
	if err != nil {
		return nil, "", err
	}
	ws, repo, err := RequireRepo(cfg)
	if err != nil {
		return nil, "", err
	}
	return c, "/workspaces/" + ws + "/repos/" + repo + "/config", nil
}

// fetchConfig GETs the current stored YAML and parses it.
func fetchConfig(c *APIClient, path string) (*config.AppConfig, error) {
	var resp struct{ Config string }
	if err := c.Do("GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Config == "" {
		return &config.AppConfig{}, nil
	}
	return config.ParseAppConfig([]byte(resp.Config))
}

// saveConfig serializes, PUTs the config back, and prints any validation warnings.
func saveConfig(c *APIClient, path string, cfg *config.AppConfig) error {
	data, err := config.MarshalAppConfig(cfg)
	if err != nil {
		return err
	}
	var resp struct {
		Config   string   `json:"config"`
		Warnings []string `json:"warnings"`
	}
	if err := c.Do("PUT", path, map[string]any{"config": string(data)}, &resp); err != nil {
		return err
	}
	for _, w := range resp.Warnings {
		fmt.Printf("  warning: %s\n", w)
	}
	return nil
}

// mutateConfig is the fetch-mutate-save cycle. Every config_* file uses this.
func mutateConfig(fn func(cfg *config.AppConfig) error) error {
	c, path, err := configClient()
	if err != nil {
		return err
	}
	cfg, err := fetchConfig(c, path)
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return saveConfig(c, path, cfg)
}

// NewConfigCmd returns the top-level config command.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage stored config",
	}

	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(NewServiceCmd())
	cmd.AddCommand(NewServerCmd())
	cmd.AddCommand(NewVolumeCmd())
	cmd.AddCommand(NewDatabaseConfigCmd())
	cmd.AddCommand(NewSecretCmd())
	cmd.AddCommand(NewStorageConfigCmd())
	cmd.AddCommand(NewBuildConfigCmd())
	cmd.AddCommand(NewCronConfigCmd())
	cmd.AddCommand(NewDomainCmd())
	cmd.AddCommand(NewFirewallCmd())
	cmd.AddCommand(NewProviderConfigCmd())

	return cmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show stored YAML config",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, path, err := configClient()
			if err != nil {
				return err
			}
			var resp struct{ Config string }
			if err := c.Do("GET", path, nil, &resp); err != nil {
				return err
			}
			if resp.Config == "" {
				fmt.Println("# no config stored yet")
				return nil
			}
			fmt.Print(resp.Config)
			return nil
		},
	}
}
