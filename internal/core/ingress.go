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
	cmd.AddCommand(newIngressSetCmd())
	cmd.AddCommand(newIngressDeleteCmd())
	return cmd
}

func newIngressSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set service:domain,domain",
		Short: "Add or update a single ingress route",
		Long: `Adds or updates an ingress route for a service. Reads the current Caddyfile,
merges the new route, and redeploys Caddy.

Examples:
  nvoi ingress set web:example.com
  nvoi ingress set web:example.com,www.example.com --cloudflare-managed
  nvoi ingress set web:example.com --cert cert.pem --key key.pem`,
		Args: cobra.ExactArgs(1),
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
			route := routes[0]

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

			// Resolve DNS provider ref.
			dnsProviderName, _ := resolveDNSProvider(cmd)
			var dnsCreds map[string]string
			if dnsProviderName != "" {
				dnsCreds, _ = resolveDNSCredentials(cmd, dnsProviderName)
			}
			if cloudflareManaged {
				route.EdgeProxied = true
			}

			return app.IngressSet(cmd.Context(), app.IngressSetRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      out,
				},
				DNS:          app.ProviderRef{Name: dnsProviderName, Creds: dnsCreds},
				Route:        route,
				EdgeProvider: resolveEdgeProvider(cloudflareManaged),
				CertPEM:      certPEM,
				KeyPEM:       keyPEM,
			})
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

func resolveEdgeProvider(cloudflareManaged bool) string {
	if cloudflareManaged {
		return "cloudflare"
	}
	return ""
}

func newIngressDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete service:domain,domain",
		Short: "Remove a single ingress route",
		Long: `Removes an ingress route for a service. Reads the current Caddyfile,
removes the route, and redeploys Caddy with remaining routes.

Use --cloudflare-managed to also revoke the Origin CA certificate at Cloudflare.

Examples:
  nvoi ingress delete web:example.com -y
  nvoi ingress delete web:example.com --cloudflare-managed -y`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Delete ingress route %s? [y/N] ", args[0])
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

			routes, err := app.ParseIngressArgs(args)
			if err != nil {
				return err
			}
			route := routes[0]

			cloudflareManaged, _ := cmd.Flags().GetBool("cloudflare-managed")

			var dnsProviderName string
			var dnsCreds map[string]string
			if cloudflareManaged {
				dnsProviderName, _ = resolveDNSProvider(cmd)
				if dnsProviderName != "" {
					dnsCreds, _ = resolveDNSCredentials(cmd, dnsProviderName)
				}
			}

			err = app.IngressDelete(cmd.Context(), app.IngressDeleteRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				DNS:   app.ProviderRef{Name: dnsProviderName, Creds: dnsCreds},
				Route: route,
			})
			return render.HandleDeleteResult(err, resolveOutput(cmd))
		},
	}
	addComputeProviderFlags(cmd)
	addDNSProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	cmd.Flags().Bool("cloudflare-managed", false, "revoke Cloudflare Origin CA cert on delete")
	return cmd
}
