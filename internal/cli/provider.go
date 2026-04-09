package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/cobra"
)

func newProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Manage infrastructure providers (workspace-scoped)",
	}
	cmd.AddCommand(newProviderSetCmd())
	cmd.AddCommand(newProviderListCmd())
	cmd.AddCommand(newProviderDeleteCmd())
	return cmd
}

// resolveProviderCredentials reads credentials from env vars and explicit KEY=VALUE args.
// Uses the provider schema to map env var names to schema keys.
// Explicit args override env vars. Missing required fields are errors.
func resolveProviderCredentials(kind, name string, args []string) (map[string]string, error) {
	schema, err := provider.GetSchema(kind, name)
	if err != nil {
		return nil, err
	}

	// Parse explicit KEY=VALUE overrides.
	overrides := map[string]string{}
	for _, arg := range args {
		k, v, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, fmt.Errorf("invalid credential %q — expected KEY=VALUE", arg)
		}
		overrides[k] = v
	}

	// Resolve each schema field: explicit override → env var.
	creds := map[string]string{}
	for _, f := range schema.Fields {
		// Check explicit override by env var name or schema key.
		if v, ok := overrides[f.EnvVar]; ok {
			creds[f.Key] = v
			continue
		}
		if v, ok := overrides[f.Key]; ok {
			creds[f.Key] = v
			continue
		}
		// Fall back to env var.
		if v := os.Getenv(f.EnvVar); v != "" {
			creds[f.Key] = v
			continue
		}
		if f.Required {
			return nil, fmt.Errorf("missing required credential %s (env var: %s)", f.Key, f.EnvVar)
		}
	}

	return creds, nil
}

func newProviderSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <kind> <name> [KEY=VALUE ...]",
		Short: "Set a provider with credentials",
		Long: `Creates or updates an infrastructure provider in the active workspace.

Kind: compute, dns, storage, build
Name: hetzner, cloudflare, aws, daytona, github, local, scaleway

Credentials are resolved from env vars automatically using the provider schema.
Explicit KEY=VALUE args override env vars.

Examples:
  nvoi provider set compute hetzner                    # reads HETZNER_TOKEN from env
  nvoi provider set dns cloudflare                     # reads CF_API_KEY, CF_ZONE_ID, DNS_ZONE from env
  nvoi provider set build daytona                      # reads DAYTONA_API_KEY from env
  nvoi provider set compute hetzner HETZNER_TOKEN=xxx  # explicit override`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			kind := args[0]
			name := args[1]

			creds, err := resolveProviderCredentials(kind, name, args[2:])
			if err != nil {
				return err
			}

			credsJSON, err := json.Marshal(creds)
			if err != nil {
				return err
			}

			body := struct {
				Kind        api.ProviderKind `json:"kind"`
				Name        string           `json:"name"`
				Credentials string           `json:"credentials"`
			}{
				Kind:        api.ProviderKind(kind),
				Name:        name,
				Credentials: string(credsJSON),
			}

			var resp api.InfraProvider
			path := "/workspaces/" + wsID + "/providers"
			if err := client.Do("POST", path, body, &resp); err != nil {
				return err
			}

			fmt.Printf("provider %s %s set (%s)\n", kind, name, resp.ID)
			return nil
		},
	}
}

func newProviderListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List providers in the active workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			var providers []struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
				Name string `json:"name"`
			}
			path := "/workspaces/" + wsID + "/providers"
			if err := client.Do("GET", path, nil, &providers); err != nil {
				return err
			}

			if len(providers) == 0 {
				fmt.Println("no providers configured")
				return nil
			}
			for _, p := range providers {
				fmt.Printf("%-10s %-15s %s\n", p.Kind, p.Name, p.ID)
			}
			return nil
		},
	}
}

func newProviderDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <kind> <name>",
		Short: "Delete a provider from the workspace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			kind := args[0]
			name := args[1]
			path := "/workspaces/" + wsID + "/providers/" + esc(kind) + "/" + esc(name)
			if err := client.Do("DELETE", path, nil, nil); err != nil {
				return err
			}

			fmt.Printf("provider %s %s deleted\n", kind, name)
			return nil
		},
	}
}
