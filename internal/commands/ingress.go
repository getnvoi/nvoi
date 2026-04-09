package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewIngressCmd returns the ingress command group.
func NewIngressCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingress",
		Short: "Manage Caddy ingress",
	}
	cmd.AddCommand(newIngressSetCmd(b))
	cmd.AddCommand(newIngressDeleteCmd(b))
	return cmd
}

func newIngressSetCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set service:domain,domain [...]",
		Short: "Add or update ingress routes",
		Long: `Adds or updates ingress routes for services. Reads the current Caddyfile,
merges the new routes, and redeploys Caddy.

Examples:
  nvoi ingress set web:example.com
  nvoi ingress set web:example.com,www.example.com --cloudflare-managed
  nvoi ingress set web:example.com api:api.example.com
  nvoi ingress set web:example.com --cert cert.pem --key key.pem`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			routes, err := parseRouteArgs(args)
			if err != nil {
				return err
			}

			certPath, _ := cmd.Flags().GetString("cert")
			keyPath, _ := cmd.Flags().GetString("key")
			cloudflareManaged, _ := cmd.Flags().GetBool("cloudflare-managed")

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

			return b.IngressSet(cmd.Context(), routes, cloudflareManaged, certPEM, keyPEM)
		},
	}
	cmd.Flags().Bool("cloudflare-managed", false, "enable Cloudflare-managed ingress and origin handling")
	cmd.Flags().String("cert", "", "TLS certificate PEM file (custom cert, skips ACME)")
	cmd.Flags().String("key", "", "TLS private key PEM file (required with --cert)")
	return cmd
}

func newIngressDeleteCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete service:domain,domain [...]",
		Short: "Remove ingress routes",
		Long: `Removes ingress routes for services. Reads the current Caddyfile,
removes the routes, and redeploys Caddy with remaining routes.

Use --cloudflare-managed to also revoke the Origin CA certificate at Cloudflare.

Examples:
  nvoi ingress delete web:example.com
  nvoi ingress delete web:example.com api:api.example.com --cloudflare-managed`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			routes, err := parseRouteArgs(args)
			if err != nil {
				return err
			}
			cloudflareManaged, _ := cmd.Flags().GetBool("cloudflare-managed")
			return b.IngressDelete(cmd.Context(), routes, cloudflareManaged)
		},
	}
	cmd.Flags().Bool("cloudflare-managed", false, "revoke Cloudflare Origin CA cert on delete")
	return cmd
}
