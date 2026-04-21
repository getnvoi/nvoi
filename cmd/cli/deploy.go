package main

import (
	"os"

	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/cobra"
)

// newDeployCmd wires `nvoi deploy`. The RunE picks between an in-process
// deploy (reconcile.Deploy) and a BuildProvider.Dispatch to a remote
// substrate based on `providers.build` in nvoi.yaml.
//
// The --local flag forces the in-process path regardless of config. Two
// callers rely on this:
//
//  1. The operator, for a quick deploy that skips whatever remote builder
//     is declared (debugging, cold-start, air-gapped laptop).
//  2. The remote builder itself (PR-B) — the outer `ssh` / `daytona`
//     BuildProvider SSHes to a builder server and invokes
//     `nvoi deploy --local` as the recursion base case. Without --local
//     the remote `nvoi` would read the same `providers.build: ssh` and
//     loop back to the dispatcher forever.
func newDeployCmd(rt *runtime) *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy from config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			if local {
				return reconcile.Deploy(cmd.Context(), rt.dc, rt.cfg)
			}

			name := rt.cfg.Providers.Build
			if name == "" {
				name = "local"
			}

			// `local` runs the reconciler in-process. LocalBuilder.Dispatch
			// is a safety-net error — routing through ResolveBuild here
			// would fire it, so we short-circuit BEFORE touching the
			// registry.
			if name == "local" {
				return reconcile.Deploy(cmd.Context(), rt.dc, rt.cfg)
			}

			// Non-local builder — resolve its creds via the same
			// CredentialSource every other provider uses, then dispatch.
			// The provider implementation reads ConfigPath + Env on the
			// remote side and re-invokes `nvoi deploy --local` there.
			creds, err := resolveProviderCreds(rt.dc.Creds, "build", name)
			if err != nil {
				return err
			}
			b, err := provider.ResolveBuild(name, creds)
			if err != nil {
				return err
			}
			defer b.Close()

			configPath, _ := cmd.Flags().GetString("config")
			return b.Dispatch(cmd.Context(), provider.BuildDispatch{
				ConfigPath: configPath,
				Env:        os.Environ(),
				Sink:       rt.out,
			})
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "bypass providers.build and run the deploy in-process")
	return cmd
}
