package commands

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/managed"
	"github.com/spf13/cobra"
)

// resolveManagedKind reads --type from the command and validates it against
// the registered kinds for the given category. Shared by database and agent commands.
func resolveManagedKind(cmd *cobra.Command, category string) (string, error) {
	kind, _ := cmd.Flags().GetString("type")
	if kind == "" {
		available := managed.KindsForCategory(category)
		return "", fmt.Errorf("--type is required. Available %s types: %s", category, strings.Join(available, ", "))
	}
	return kind, nil
}

// parseRouteArgs parses service:domain,domain args into RouteArg slices.
// Delegates to pkg/core.ParseIngressArgs for the actual parsing.
func parseRouteArgs(args []string) ([]RouteArg, error) {
	parsed, err := core.ParseIngressArgs(args)
	if err != nil {
		return nil, err
	}
	routes := make([]RouteArg, len(parsed))
	for i, p := range parsed {
		routes[i] = RouteArg{Service: p.Service, Domains: p.Domains}
	}
	return routes, nil
}
