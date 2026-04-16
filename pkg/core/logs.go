package core

import (
	"context"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
)

type LogsRequest struct {
	Cluster
	Service    string
	Follow     bool
	Tail       int
	Since      string // duration: "5m", "1h", "24h"
	Previous   bool
	Timestamps bool
}

func Logs(ctx context.Context, req LogsRequest) error {
	names, err := req.Cluster.Names()
	if err != nil {
		return err
	}

	ns := names.KubeNamespace()

	pod, err := req.Kube.FirstPod(ctx, ns, req.Service)
	if err != nil {
		return err
	}

	opts := &corev1.PodLogOptions{
		Follow:     req.Follow,
		Previous:   req.Previous,
		Timestamps: req.Timestamps,
	}
	if req.Tail > 0 {
		t := int64(req.Tail)
		opts.TailLines = &t
	}
	if req.Since != "" {
		if d, err := parseDuration(req.Since); err == nil {
			secs := int64(d.Seconds())
			opts.SinceSeconds = &secs
		}
	}

	w := req.Log().Writer()
	return req.Kube.StreamLogs(ctx, ns, pod, opts, w)
}

// parseDuration parses Go-style duration or kubectl-style duration (e.g. "5m", "1h", "24h").
func parseDuration(s string) (time.Duration, error) {
	// Try Go stdlib first
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	// kubectl accepts bare numbers as seconds
	if secs, err := strconv.Atoi(s); err == nil {
		return time.Duration(secs) * time.Second, nil
	}
	return 0, ErrInputf("invalid duration %q", s)
}
