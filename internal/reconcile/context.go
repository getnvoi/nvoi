// Package reconcile implements the deploy/destroy orchestrator.
// Each resource type has a reconcile function: add desired, remove orphans.
// The reconciler calls pkg/core/ directly — no interface, no adapter.
package reconcile

import app "github.com/getnvoi/nvoi/pkg/core"

// DeployContext holds everything needed to execute against a cluster.
type DeployContext struct {
	Cluster     app.Cluster
	DNS         app.ProviderRef
	Storage     app.ProviderRef
	Builder     string
	BuildCreds  map[string]string
	GitUsername string
	GitToken    string
}

// LiveState represents what's currently deployed.
type LiveState struct {
	Servers  []string
	Services []string
	Crons    []string
	Volumes  []string
	Storage  []string
	Secrets  []string
	Domains  map[string][]string
}
