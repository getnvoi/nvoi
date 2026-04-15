package cloud

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/cobra"
)

func NewProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Manage infrastructure providers (workspace-scoped)",
	}
	cmd.AddCommand(newProviderAddCmd())
	cmd.AddCommand(newProviderListCmd())
	cmd.AddCommand(newProviderDeleteCmd())
	return cmd
}

// resolveProviderCredentials reads credentials from env vars and explicit KEY=VALUE args.
// Uses the provider schema to map env var names to schema keys.
// Explicit args override env vars. Missing required fields are errors.
func resolveProviderCredentials(kind, providerName string, args []string) (map[string]string, error) {
	schema, err := provider.GetSchema(kind, providerName)
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

func newProviderAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <provider> [KEY=VALUE ...]",
		Short: "Add a provider with credentials",
		Long: `Adds an infrastructure provider to the active workspace.

Provider: hetzner, cloudflare, aws, daytona, github, scaleway, doppler, awssm, infisical
Kind: compute, dns, storage, build, secrets
Name: user-chosen alias for linking to repos (defaults to provider name)

Credentials are resolved from env vars automatically using the provider schema.
Explicit KEY=VALUE args override env vars.

Examples:
  nvoi provider add hetzner --kind compute --name hetzner-prod HETZNER_TOKEN=xxx
  nvoi provider add cloudflare --kind dns --name cf-dns CF_API_KEY=xxx CF_ZONE_ID=xxx DNS_ZONE=nvoi.to
  nvoi provider add cloudflare --kind storage --name cf-storage CF_ACCOUNT_ID=xxx CF_R2_ACCESS_KEY_ID=xxx CF_R2_SECRET_ACCESS_KEY=xxx
  nvoi provider add daytona --kind build --name daytona-team DAYTONA_API_KEY=xxx
  nvoi provider add doppler --kind secrets DOPPLER_TOKEN=xxx DOPPLER_PROJECT=myapp DOPPLER_CONFIG=production
  nvoi provider add hetzner --kind compute HETZNER_TOKEN=xxx   # alias defaults to "hetzner"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := AuthedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			providerName := args[0]
			kind, _ := cmd.Flags().GetString("kind")
			if kind == "" {
				return fmt.Errorf("--kind is required (compute, dns, storage, build, secrets)")
			}
			name, _ := cmd.Flags().GetString("name")
			if name == "" {
				name = providerName // default alias = provider name
			}

			creds, err := resolveProviderCredentials(kind, providerName, args[1:])
			if err != nil {
				return err
			}

			credsJSON, err := json.Marshal(creds)
			if err != nil {
				return err
			}

			body := struct {
				Alias       string           `json:"alias"`
				Kind        api.ProviderKind `json:"kind"`
				Provider    string           `json:"provider"`
				Credentials string           `json:"credentials"`
			}{
				Alias:       name,
				Kind:        api.ProviderKind(kind),
				Provider:    providerName,
				Credentials: string(credsJSON),
			}

			var resp struct {
				Created bool `json:"created"`
			}
			path := "/workspaces/" + wsID + "/providers"
			if err := client.Do("POST", path, body, &resp); err != nil {
				return err
			}

			action := "updated"
			if resp.Created {
				action = "created"
			}
			fmt.Printf("provider %s %s (%s %s)\n", name, action, kind, providerName)
			return nil
		},
	}
	cmd.Flags().String("kind", "", "provider domain (compute, dns, storage, build)")
	cmd.Flags().String("name", "", "alias for this provider (defaults to provider name)")
	return cmd
}

func newProviderListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List providers in the active workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := AuthedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			var providers []struct {
				ID       string `json:"id"`
				Alias    string `json:"alias"`
				Kind     string `json:"kind"`
				Provider string `json:"provider"`
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
				fmt.Printf("%-20s %-10s %s\n", p.Alias, p.Kind, p.Provider)
			}
			return nil
		},
	}
}

func newProviderDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <alias>",
		Short: "Delete a provider from the workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := AuthedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			alias := args[0]
			path := "/workspaces/" + wsID + "/providers/" + esc(alias)
			if err := client.Do("DELETE", path, nil, nil); err != nil {
				return err
			}

			fmt.Printf("provider %s deleted\n", alias)
			return nil
		},
	}
}
