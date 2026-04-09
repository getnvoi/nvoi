package commands

import "github.com/getnvoi/nvoi/pkg/core"

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
