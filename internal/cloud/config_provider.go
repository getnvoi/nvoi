package cloud

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewProviderConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "provider", Short: "Manage providers in config"}
	cmd.AddCommand(newProviderConfigSetCmd())
	return cmd
}

func newProviderConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <kind> <alias>",
		Short: "Link a workspace provider to this repo's config",
		Long:  "Resolves the provider name from the workspace alias, writes it into the stored config, and links the credentials to the repo.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, alias := args[0], args[1]

			switch kind {
			case "compute", "dns", "storage", "build":
			default:
				return fmt.Errorf("unknown provider kind %q — must be compute, dns, storage, or build", kind)
			}

			c, authCfg, err := AuthedClient()
			if err != nil {
				return err
			}
			ws, repo, err := RequireRepo(authCfg)
			if err != nil {
				return err
			}

			// List workspace providers, find the alias.
			var providers []struct {
				Alias    string `json:"alias"`
				Kind     string `json:"kind"`
				Provider string `json:"provider"`
			}
			if err := c.Do("GET", "/workspaces/"+ws+"/providers", nil, &providers); err != nil {
				return err
			}
			var providerName string
			for _, p := range providers {
				if p.Alias == alias && p.Kind == kind {
					providerName = p.Provider
					break
				}
			}
			if providerName == "" {
				return fmt.Errorf("provider alias %q (kind: %s) not found in workspace", alias, kind)
			}

			// Link credentials to repo.
			if err := c.Do("PUT", "/workspaces/"+ws+"/repos/"+repo, map[string]any{
				kind + "_provider": alias,
			}, nil); err != nil {
				return fmt.Errorf("link provider: %w", err)
			}

			// Write provider name into stored config.
			base := "/workspaces/" + ws + "/repos/" + repo + "/config"
			cfg, err := fetchConfig(c, base)
			if err != nil {
				return err
			}

			switch kind {
			case "compute":
				cfg.Providers.Compute = providerName
			case "dns":
				cfg.Providers.DNS = providerName
			case "storage":
				cfg.Providers.Storage = providerName
			case "build":
				cfg.Providers.Build = providerName
			}

			if err := saveConfig(c, base, cfg); err != nil {
				return err
			}

			fmt.Printf("provider %s set to %q (alias: %s)\n", kind, providerName, alias)
			return nil
		},
	}
}
