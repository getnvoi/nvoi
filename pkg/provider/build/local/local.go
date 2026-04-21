// Package local is the default build provider. It runs reconcile.Deploy
// in-process on the operator's machine — the exact behavior nvoi has had
// since day one. Registered so capability bits are addressable by name
// alongside the remote substrates (ssh, daytona) that land in later PRs.
//
// Dispatch is intentionally a safety-net error: cmd/cli/deploy.go routes
// the "local" path directly to reconcile.Deploy without calling Dispatch.
// A call here means the dispatch wiring regressed.
package local

import (
	"context"
	"errors"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// errDispatchCalled is returned by LocalBuilder.Dispatch if anything ever
// routes through it. The in-process path in cmd/cli/deploy.go calls
// reconcile.Deploy directly for the "local" provider name — if Dispatch
// fires, the CLI wiring is wrong.
var errDispatchCalled = errors.New("build: local runs in-process; Dispatch must not be called — cmd/cli/deploy.go should route directly to reconcile.Deploy for `providers.build: local`")

// LocalBuilder is the registered BuildProvider for name "local".
type LocalBuilder struct{}

// Dispatch is a safety-net error — see package doc.
func (LocalBuilder) Dispatch(ctx context.Context, req provider.BuildDispatch) error {
	return errDispatchCalled
}

// Close is a no-op; no resources held.
func (LocalBuilder) Close() error { return nil }

func init() {
	provider.RegisterBuild(
		"local",
		provider.CredentialSchema{Name: "local"}, // no credentials — runs in-process
		provider.BuildCapability{
			RequiresBuilders:   false, // local can't use a remote builder by definition
			DispatchableFromCI: false, // CI needs a remote substrate to dispatch to
		},
		func(_ map[string]string) provider.BuildProvider { return LocalBuilder{} },
	)
}
