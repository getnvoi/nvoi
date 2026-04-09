package core

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/commands"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) DNSSet(ctx context.Context, routes []commands.RouteArg, cloudflareManaged bool) error {
	for _, route := range routes {
		if err := app.DNSSet(ctx, app.DNSSetRequest{
			Cluster:           d.cluster,
			DNS:               d.dns,
			Service:           route.Service,
			Domains:           route.Domains,
			CloudflareManaged: cloudflareManaged,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (d *DirectBackend) DNSDelete(ctx context.Context, routes []commands.RouteArg) error {
	for _, route := range routes {
		err := app.DNSDelete(ctx, app.DNSDeleteRequest{
			Cluster: d.cluster,
			DNS:     d.dns,
			Service: route.Service,
			Domains: route.Domains,
		})
		if rerr := d.handleDelete(err); rerr != nil {
			return rerr
		}
	}
	return nil
}

func (d *DirectBackend) DNSList(ctx context.Context) error {
	records, err := app.DNSList(ctx, app.DNSListRequest{
		DNS:    d.dns,
		Output: d.cluster.Output,
	})
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("no records")
		return nil
	}
	for _, r := range records {
		fmt.Printf("%-4s %-40s %s\n", r.Type, r.Domain, r.IP)
	}
	return nil
}
