package core

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// TestClassify_AllFourStates locks the four-state rule end-to-end at
// the consumer-side classifier — the only place that knows about
// ownership. Provider package emits rows; pkg/core stamps Ownership.
func TestClassify_AllFourStates(t *testing.T) {
	ctx := &provider.OwnershipContext{
		AppEnv:          "nvoi-myapp-prod",
		ExpectedServers: map[string]bool{"nvoi-myapp-prod-master": true},
	}
	groups := []provider.ResourceGroup{{
		Name:    "Servers",
		Columns: []string{"ID", "Name", "Status"},
		Rows: [][]string{
			{"1", "nvoi-myapp-prod-master", "running"},     // live
			{"2", "nvoi-myapp-prod-old-worker", "running"}, // stale
			{"3", "nvoi-other-prod-master", "running"},     // other
			{"4", "manual-server", "running"},              // no
			{"5", "nvoi-releases", "running"},              // no (2 segs)
		},
	}}
	Classify(groups, ctx)

	got := groups[0].Ownership
	want := []provider.Ownership{
		provider.OwnershipLive,
		provider.OwnershipStale,
		provider.OwnershipOther,
		provider.OwnershipNone,
		provider.OwnershipNone,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row[%d] (%s) = %q, want %q", i, groups[0].Rows[i][1], got[i], want[i])
		}
	}
}

// TestClassify_DNSUsesCfgMatch — DNS records don't follow the nvoi
// naming pattern, so classification falls back to ClassifyByCfgMatch:
// in cfg → live, otherwise → no. Other/stale unavailable for DNS.
func TestClassify_DNSUsesCfgMatch(t *testing.T) {
	ctx := &provider.OwnershipContext{
		AppEnv:      "nvoi-myapp-prod",
		ExpectedDNS: map[string]bool{"api.example.com": true},
	}
	groups := []provider.ResourceGroup{{
		Name:    "DNS Records",
		Columns: []string{"Type", "Domain", "Target"},
		Rows: [][]string{
			{"A", "api.example.com", "1.2.3.4"},      // live
			{"A", "manual.example.com", "5.6.7.8"},   // no
			{"CNAME", "api.example.com", "tunnel.x"}, // live (same domain)
		},
	}}
	Classify(groups, ctx)

	want := []provider.Ownership{
		provider.OwnershipLive,
		provider.OwnershipNone,
		provider.OwnershipLive,
	}
	for i := range want {
		if groups[0].Ownership[i] != want[i] {
			t.Errorf("row[%d] = %q, want %q", i, groups[0].Ownership[i], want[i])
		}
	}
}

// TestClassify_BucketDoesntMatchOnSinglePrefix locks the regression:
// `nvoi-releases` (2 segments) must NOT classify as anything but `no`,
// even though it starts with the prefix. Real R2 buckets named
// manually like that were the original false-positive.
func TestClassify_BucketDoesntMatchOnSinglePrefix(t *testing.T) {
	ctx := &provider.OwnershipContext{
		AppEnv:          "nvoi-nvoi-production",
		ExpectedBuckets: map[string]bool{"nvoi-nvoi-production-db-main-backups": true},
	}
	groups := []provider.ResourceGroup{{
		Name:    "R2 Buckets",
		Columns: []string{"Name"},
		Rows: [][]string{
			{"nvoi-nvoi-production-db-main-backups"},    // live
			{"nvoi-nvoi-production-bugsink-db-backups"}, // stale (this app+env, not in cfg)
			{"nvoi-other-app-prod-something"},           // other (different app+env)
			{"nvoi-releases"},                           // no (2 segs — manual naming)
			{"fastmag-production-backend"},              // no (no prefix)
		},
	}}
	Classify(groups, ctx)

	want := []provider.Ownership{
		provider.OwnershipLive,
		provider.OwnershipStale,
		provider.OwnershipOther,
		provider.OwnershipNone,
		provider.OwnershipNone,
	}
	for i := range want {
		if groups[0].Ownership[i] != want[i] {
			t.Errorf("row[%d] (%s) = %q, want %q", i, groups[0].Rows[i][0], groups[0].Ownership[i], want[i])
		}
	}
}

// TestClassify_TunnelExactMatchOrOther — tunnel name is exactly
// AppEnv (Names.Base()). Anything else nvoi-shaped → other; non-nvoi → no.
func TestClassify_TunnelExactMatchOrOther(t *testing.T) {
	ctx := &provider.OwnershipContext{
		AppEnv:          "nvoi-myapp-prod",
		ExpectedTunnels: map[string]bool{"nvoi-myapp-prod": true},
	}
	groups := []provider.ResourceGroup{{
		Name:    "Cloudflare Tunnels",
		Columns: []string{"ID", "Name", "Status"},
		Rows: [][]string{
			{"t1", "nvoi-myapp-prod", "healthy"}, // live
			{"t2", "nvoi-other-prod", "down"},    // other
			{"t3", "dev-hostzero", "down"},       // no
			{"t4", "nvoi-releases", "down"},      // no
		},
	}}
	Classify(groups, ctx)
	want := []provider.Ownership{
		provider.OwnershipLive,
		provider.OwnershipOther,
		provider.OwnershipNone,
		provider.OwnershipNone,
	}
	for i := range want {
		if groups[0].Ownership[i] != want[i] {
			t.Errorf("row[%d] (%s) = %q, want %q", i, groups[0].Rows[i][1], groups[0].Ownership[i], want[i])
		}
	}
}

// TestClassify_NilContext_AllOther — when the CLI has no cfg loaded
// (e.g. running `nvoi resources` without nvoi.yaml), everything
// nvoi-shaped classifies as OwnershipOther; everything else as None.
func TestClassify_NilContext_AllOther(t *testing.T) {
	groups := []provider.ResourceGroup{{
		Name:    "Servers",
		Columns: []string{"ID", "Name"},
		Rows: [][]string{
			{"1", "nvoi-myapp-prod-master"},
			{"2", "manual-server"},
		},
	}}
	Classify(groups, nil)
	if got := groups[0].Ownership[0]; got != provider.OwnershipOther {
		t.Errorf("nvoi-named row with nil ctx = %q, want other", got)
	}
	if got := groups[0].Ownership[1]; got != provider.OwnershipNone {
		t.Errorf("manual row with nil ctx = %q, want no", got)
	}
}

// TestClassify_UnknownGroupSkipped — groups whose Name isn't in the
// classifier mapping (Subnets, Route Tables, github-actions-secrets,
// etc.) get no Ownership column at all.
func TestClassify_UnknownGroupSkipped(t *testing.T) {
	groups := []provider.ResourceGroup{{
		Name:    "Subnets",
		Columns: []string{"ID", "Name"},
		Rows:    [][]string{{"sub-1", "default"}},
	}}
	Classify(groups, &provider.OwnershipContext{AppEnv: "nvoi-myapp-prod"})
	if groups[0].Ownership != nil {
		t.Errorf("unknown group must skip Ownership; got %v", groups[0].Ownership)
	}
}
