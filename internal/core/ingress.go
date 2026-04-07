package core

import (
	"fmt"
	"os"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newIngressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingress",
		Short: "Manage Caddy ingress",
	}
	cmd.AddCommand(newIngressApplyCmd())
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
  nvoi ingress apply web:example.com --proxy   # auto-generates Cloudflare Origin CA cert`,
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

			proxy, _ := cmd.Flags().GetBool("proxy")
			if proxy {
				for i := range routes {
					routes[i].Proxy = true
				}
			}
			certPath, _ := cmd.Flags().GetString("cert")
			keyPath, _ := cmd.Flags().GetString("key")

			out := resolveOutput(cmd)

			// Resolve cert + key
			var certPEM, keyPEM string

			if certPath != "" && keyPath != "" {
				// Bring your own cert
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
			} else if proxy {
				// Auto-generate Cloudflare Origin CA cert
				cert, key, err := resolveOriginCACert(cmd, routes, out)
				if err != nil {
					return err
				}
				certPEM = cert
				keyPEM = key
			}

			return app.IngressApply(cmd.Context(), app.IngressApplyRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      out,
				},
				Routes:  routes,
				CertPEM: certPEM,
				KeyPEM:  keyPEM,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addDNSProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().Bool("proxy", false, "Cloudflare proxy mode — auto-generates Origin CA cert, firewall coherence check")
	cmd.Flags().String("cert", "", "TLS certificate PEM file (custom cert, skips ACME)")
	cmd.Flags().String("key", "", "TLS private key PEM file (required with --cert)")
	return cmd
}
