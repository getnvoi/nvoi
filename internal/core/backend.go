package core

import (
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
)

// DirectBackend implements commands.Backend by calling pkg/core/ functions directly.
// All provider/credential resolution happens once at construction via buildDirectBackend.
type DirectBackend struct {
	cluster      app.Cluster
	dns          app.ProviderRef
	storage      app.ProviderRef
	builder      string
	builderCreds map[string]string
	gitUsername  string
	gitToken     string
}

// handleDelete is the shared delete-then-render pattern.
func (d *DirectBackend) handleDelete(err error) error {
	return render.HandleDeleteResult(err, d.cluster.Output)
}

// clusterWith returns a copy of d.cluster with overridden credentials.
func (d *DirectBackend) clusterWith(creds map[string]string) app.Cluster {
	c := d.cluster
	c.Credentials = creds
	return c
}

func copyMap(m map[string]string) map[string]string {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
