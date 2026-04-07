package core

import (
	"fmt"
	"os"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newIngressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingress",
		Short: "Manage Caddy ingress",
	}
	cmd.AddCommand(newIngressApplyCmd())
	cmd.AddCommand(newIngressDeleteCmd())
	return cmd
}

func newIngressApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply [service:domain,domain ...]",
		Short: "Deploy Caddy with all specified routes in a single rollout",
		Long: `Builds the Caddyfile from service:domain mappings and deploys Caddy once.

Examples:
  nvoi ingress apply web:example.com api:api.example.com
  nvoi ingress apply web:example.com --cert cert.pem --key key.pem
  nvoi ingress apply web:example.com --cloudflare-managed`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			computeProvider, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			computeCreds, err := resolveComputeCredentials(cmd, computeProvider)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			routes, err := app.ParseIngressArgs(args)
			if err != nil {
				return err
			}

			certPath, _ := cmd.Flags().GetString("cert")
			keyPath, _ := cmd.Flags().GetString("key")
			cloudflareManaged, _ := cmd.Flags().GetBool("cloudflare-managed")

			out := resolveOutput(cmd)

			// Resolve cert + key (BYO cert only — auto-generation handled by pkg/core)
			var certPEM, keyPEM string

			if certPath != "" && keyPath != "" {
				certData, err := os.ReadFile(certPath)
				if err != nil {
					return fmt.Errorf("read cert: %w", err)
				}
				keyData, err := os.ReadFile(keyPath)
				if err != nil {
					return fmt.Errorf("read key: %w", err)
				}
				certPEM = string(certData)
				keyPEM = string(keyData)
			} else if certPath != "" || keyPath != "" {
				return fmt.Errorf("--cert and --key must both be provided")
			}

			// Resolve DNS provider ref — needed for overlay TLS helpers when explicitly used.
			dnsProviderName, _ := resolveDNSProvider(cmd)
			var dnsCreds map[string]string
			if dnsProviderName != "" {
				dnsCreds, _ = resolveDNSCredentials(cmd, dnsProviderName)
			}
			if cloudflareManaged {
				for i := range routes {
					routes[i].EdgeProxied = true
				}
			}

			err = app.IngressApply(cmd.Context(), app.IngressApplyRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      out,
				},
				DNS:          app.ProviderRef{Name: dnsProviderName, Creds: dnsCreds},
				Routes:       routes,
				TLSMode:      resolveIngressTLSMode(cloudflareManaged, certPEM, keyPEM),
				EdgeProvider: resolveIngressEdgeProvider(cloudflareManaged),
				CertPEM:      certPEM,
				KeyPEM:       keyPEM,
			})
			return err
		},
	}
	addComputeProviderFlags(cmd)
	addDNSProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().Bool("cloudflare-managed", false, "enable Cloudflare-managed ingress and origin handling")
	cmd.Flags().String("cert", "", "TLS certificate PEM file (custom cert, skips ACME)")
	cmd.Flags().String("key", "", "TLS private key PEM file (required with --cert)")
	return cmd
}

func resolveIngressTLSMode(cloudflareManaged bool, certPEM, keyPEM string) string {
	if certPEM != "" || keyPEM != "" {
		return "provided"
	}
	if cloudflareManaged {
		return "edge_origin"
	}
	return "acme"
}

func resolveIngressEdgeProvider(cloudflareManaged bool) string {
	if cloudflareManaged {
		return "cloudflare"
	}
	return ""
}

func newIngressDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove Caddy ingress",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Delete ingress? [y/N] ")
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			computeProvider, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			computeCreds, err := resolveComputeCredentials(cmd, computeProvider)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			err = app.IngressApply(cmd.Context(), app.IngressApplyRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Routes: nil,
			})
			return render.HandleDeleteResult(err, resolveOutput(cmd))
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}
