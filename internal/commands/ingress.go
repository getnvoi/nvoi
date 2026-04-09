package commands

import "github.com/spf13/cobra"

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
	return &cobra.Command{
		Use:   "set service:domain,domain [...]",
		Short: "Add or update ingress routes",
		Long: `Adds or updates ingress routes for services. Reads the current Caddyfile,
merges the new routes, and redeploys Caddy.

Examples:
  nvoi ingress set web:example.com
  nvoi ingress set web:example.com,www.example.com
  nvoi ingress set web:example.com api:api.example.com`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			routes, err := parseRouteArgs(args)
			if err != nil {
				return err
			}
			return b.IngressSet(cmd.Context(), routes)
		},
	}
}

func newIngressDeleteCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "delete service:domain,domain [...]",
		Short: "Remove ingress routes",
		Long: `Removes ingress routes for services. Reads the current Caddyfile,
removes the routes, and redeploys Caddy with remaining routes.

Examples:
  nvoi ingress delete web:example.com
  nvoi ingress delete web:example.com api:api.example.com`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			routes, err := parseRouteArgs(args)
			if err != nil {
				return err
			}
			return b.IngressDelete(cmd.Context(), routes)
		},
	}
}
