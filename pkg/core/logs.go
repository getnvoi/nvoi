package core

import (
	"context"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

type LogsRequest struct {
	Cluster
	Cfg        provider.ProviderConfigView
	Service    string
	Follow     bool
	Tail       int
	Since      string // duration: "5m", "1h", "24h"
	Previous   bool
	Timestamps bool
}

// Logs streams logs of every pod backing req.Service to the request's
// Output writer. Translates flag values into typed PodLogOptions.
func Logs(ctx context.Context, req LogsRequest) error {
	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	return kc.StreamLogs(ctx, req.Log().Writer(), kube.LogsRequest{
		Namespace:  names.KubeNamespace(),
		Selector:   kube.PodSelector(req.Service),
		Follow:     req.Follow,
		Tail:       req.Tail,
		Since:      req.Since,
		Previous:   req.Previous,
		Timestamps: req.Timestamps,
	})
}
