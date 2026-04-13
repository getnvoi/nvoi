package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewDomainCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "domain", Short: "Manage domains in config"}
	cmd.AddCommand(newDomainAddCmd())
	cmd.AddCommand(newDomainRemoveCmd())
	return cmd
}

func newDomainAddCmd() *cobra.Command {
	return &cobra.Command{
		Use: "add <service> <domain>", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				if cfg.Domains == nil {
					cfg.Domains = map[string][]string{}
				}
				svc, domain := args[0], args[1]
				for _, d := range cfg.Domains[svc] {
					if d == domain {
						fmt.Printf("domain %q already on service %q\n", domain, svc)
						return nil
					}
				}
				cfg.Domains[svc] = append(cfg.Domains[svc], domain)
				fmt.Printf("domain %q added to service %q\n", domain, svc)
				return nil
			})
		},
	}
}

func newDomainRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <service> <domain>", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				svc, domain := args[0], args[1]
				filtered := []string{}
				for _, d := range cfg.Domains[svc] {
					if d != domain {
						filtered = append(filtered, d)
					}
				}
				if len(filtered) == 0 {
					delete(cfg.Domains, svc)
				} else {
					cfg.Domains[svc] = filtered
				}
				fmt.Printf("domain %q removed from service %q\n", domain, svc)
				return nil
			})
		},
	}
}
