package core

import (
	"context"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func (d *DirectBackend) FirewallSet(ctx context.Context, args []string) error {
	if len(args) == 0 {
		if envVal := os.Getenv("NVOI_FIREWALL"); envVal != "" {
			args = strings.Split(envVal, ";")
		}
	}
	allowed, err := provider.ResolveFirewallArgs(ctx, args)
	if err != nil {
		return err
	}
	return app.FirewallSet(ctx, app.FirewallSetRequest{
		Cluster:    d.cluster,
		AllowedIPs: allowed,
	})
}

func (d *DirectBackend) FirewallList(ctx context.Context) error {
	rules, err := app.FirewallList(ctx, app.FirewallListRequest{Cluster: d.cluster})
	if err != nil {
		return err
	}
	t := render.NewTable("PORT", "ALLOWED CIDRs")
	if len(rules) == 0 {
		t.Row("*", "base rules only (SSH + internal)")
	} else {
		for _, port := range provider.SortedPorts(rules) {
			t.Row(port, strings.Join(rules[port], ", "))
		}
	}
	t.Print()
	return nil
}
