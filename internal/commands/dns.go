package commands

import "github.com/spf13/cobra"

// NewDNSCmd returns the dns command group.
func NewDNSCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Manage DNS records only",
	}
	cmd.AddCommand(newDNSSetCmd(b))
	cmd.AddCommand(newDNSDeleteCmd(b))
	cmd.AddCommand(newDNSListCmd(b))
	return cmd
}

func newDNSSetCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "set service:domain,domain [...]",
		Short: "Create or update DNS A records pointing to the service server",
		Long: `Points domains at the server running the service.

Examples:
  nvoi dns set web:myapp.com
  nvoi dns set web:myapp.com,www.myapp.com
  nvoi dns set web:myapp.com api:api.myapp.com`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			routes, err := parseRouteArgs(args)
			if err != nil {
				return err
			}
			return b.DNSSet(cmd.Context(), routes)
		},
	}
}

func newDNSDeleteCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "delete service:domain,domain [...]",
		Short: "Delete DNS records (fails if ingress still uses them)",
		Long: `Deletes DNS A records for the specified service:domain pairs.

Examples:
  nvoi dns delete web:myapp.com -y
  nvoi dns delete web:myapp.com,www.myapp.com -y
  nvoi dns delete web:myapp.com api:api.myapp.com -y`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			routes, err := parseRouteArgs(args)
			if err != nil {
				return err
			}
			return b.DNSDelete(cmd.Context(), routes)
		},
	}
}

func newDNSListCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List DNS records in zone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.DNSList(cmd.Context())
		},
	}
}
