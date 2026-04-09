package core

import (
	"context"

	"github.com/getnvoi/nvoi/internal/commands"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) IngressSet(ctx context.Context, routes []commands.RouteArg) error {
	for _, route := range routes {
		if err := app.IngressSet(ctx, app.IngressSetRequest{
			Cluster: d.cluster,
			Route:   app.IngressRouteArg{Service: route.Service, Domains: route.Domains},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (d *DirectBackend) IngressDelete(ctx context.Context, routes []commands.RouteArg) error {
	for _, route := range routes {
		err := app.IngressDelete(ctx, app.IngressDeleteRequest{
			Cluster: d.cluster,
			Route:   app.IngressRouteArg{Service: route.Service, Domains: route.Domains},
		})
		if rerr := d.handleDelete(err); rerr != nil {
			return rerr
		}
	}
	return nil
}
