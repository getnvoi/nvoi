package main

import (
	"encoding/json"
	"os"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
)

func newDescribeCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Live cluster state",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			// Workloads = every service + cron from cfg, sorted. describe
			// walks the live `{name}-secrets` Secret for each so auto-
			// injected keys (DATABASE_URL_X, storage creds) surface
			// alongside explicit secrets: declarations.
			workloads := append(utils.SortedKeys(rt.cfg.Services), utils.SortedKeys(rt.cfg.Crons)...)

			// Database probes — one per cfg.Databases entry. Resolved
			// at the cmd boundary so describe (in pkg/core) doesn't
			// need to know about credential sources. Each probe carries
			// its provider instance + a fully-populated DatabaseRequest
			// so the live SELECT 1 can fire without any extra setup.
			// Failures here (engine not registered, creds missing) are
			// fatal-for-this-DB but not for describe overall — we skip
			// the probe and the row simply won't appear.
			probes, dbCleanup := buildDescribeDatabaseProbes(cmd, rt)
			defer dbCleanup()

			req := app.DescribeRequest{
				Cluster:      rt.dc.Cluster,
				Cfg:          config.NewView(rt.cfg),
				StorageNames: rt.cfg.StorageNames(),
				Workloads:    workloads,
				Databases:    probes,
			}
			if j {
				raw, err := app.DescribeJSON(cmd.Context(), req)
				if err != nil {
					return err
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(raw)
			}
			res, err := app.Describe(cmd.Context(), req)
			if err != nil {
				return err
			}
			render.RenderDescribe(res)
			return nil
		},
	}
}

// buildDescribeDatabaseProbes constructs one app.DatabaseProbe per
// cfg.Databases entry. Mirrors resolveDatabaseCommand (cmd/cli/database.go)
// but for the read-only describe path: no backup-bucket hookup, no Log
// sink, and any per-DB resolution failure is swallowed (the row just
// drops; describe must never error out because one engine is misconfigured).
//
// Returned cleanup chains every provider's Close() + the shared kube
// client cleanup. Caller defers it.
func buildDescribeDatabaseProbes(cmd *cobra.Command, rt *runtime) ([]app.DatabaseProbe, func()) {
	noop := func() {}
	if len(rt.cfg.Databases) == 0 {
		return nil, noop
	}
	names, err := rt.dc.Cluster.Names()
	if err != nil {
		return nil, noop
	}
	sources, err := commandSources(rt)
	if err != nil {
		// Source resolution failure means $VAR refs in databases.X.credentials
		// can't be expanded — skip every probe rather than fail describe.
		return nil, noop
	}

	// Single kube client for the whole describe. Cluster.Kube caches
	// on first call so subsequent connects are no-ops; cleanup runs
	// once at the end.
	kc, _, kubeCleanup, err := rt.dc.Cluster.Kube(cmd.Context(), config.NewView(rt.cfg))
	if err != nil {
		return nil, noop
	}

	var probes []app.DatabaseProbe
	var providers []provider.DatabaseProvider
	for _, name := range utils.SortedKeys(rt.cfg.Databases) {
		def := rt.cfg.Databases[name]
		req, err := commandDatabaseRequest(name, def, names, sources)
		if err != nil {
			continue
		}
		req.Namespace = names.KubeNamespace()
		req.Labels = names.Labels()
		// Postgres ExecSQL kc.Exec's into the pod — it needs a kube
		// client. Other engines hit the SaaS API and ignore req.Kube.
		if def.Engine == "postgres" {
			req.Kube = kc
		}

		creds, err := resolveProviderCreds(rt.dc.Creds, "database", def.Engine)
		if err != nil {
			continue
		}
		prov, err := provider.ResolveDatabase(def.Engine, creds)
		if err != nil {
			continue
		}
		providers = append(providers, prov)
		probes = append(probes, app.DatabaseProbe{
			Name:     name,
			Engine:   def.Engine,
			Provider: prov,
			Request:  req,
		})
	}

	cleanup := func() {
		for _, p := range providers {
			_ = p.Close()
		}
		kubeCleanup()
	}
	return probes, cleanup
}
