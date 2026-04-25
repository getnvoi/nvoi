// Package infra holds the shared IaaS skeleton used by
// pkg/provider/{hetzner,aws,scaleway}. Polling constants, error sentinels,
// and the ensure.Delete helper live here; each provider keeps its own
// API glue but routes through this package for the bits that must not
// drift across backends.
//
// Before this package existed, each IaaS backend carried its own
// polling timeouts (90s / 2min / 5min across providers for the same
// operation), its own inline string-matching on error messages
// (`strings.Contains(err.Error(), "locked")`), and its own ad-hoc
// delete sequencing. Every divergence was a bug magnet.
package infra

import "time"

// PollInterval is the single poll cadence every IaaS operation uses.
// 3 seconds is fast enough that the operator doesn't feel idle, slow
// enough that we don't hammer provider APIs. Overriding at call sites
// is discouraged — a deviation means either the operation is wrong-
// granularity (split it) or a rate-limit pushback worth surfacing.
const PollInterval = 3 * time.Second

// PollFast is the upper-bound budget for "attach-style" operations:
// firewall attach/detach, volume attach/detach, security-group swap.
// 2 minutes covers the 99th-percentile response from all three backends.
// Going aggressive here (30s, 1min) risked false timeouts during Scaleway
// region pressure; going conservative (5min) left the operator staring at
// a hung CLI when an API had actually failed silently.
const PollFast = 2 * time.Minute

// PollSlow is the upper-bound budget for "boot-style" operations:
// server provisioning, server termination, long-running state
// transitions. Covers image-pull + cloud-init + first-boot plus a
// safety margin. Previously Hetzner used 2min (too tight — cold AMIs
// time out) and AWS used 5min (fine).
const PollSlow = 5 * time.Minute
