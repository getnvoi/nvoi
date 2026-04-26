package core

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// TestClassify_OwnedVsExternal locks the binary rule: name in
// expected-set → owned, anything else → external. Includes the
// `nvoi-releases` and `nvoi-nvoi-production-bugsink-db-backups`
// regressions: structural inference is dead, only cfg match counts.
func TestClassify_OwnedVsExternal(t *testing.T) {
	ctx := &provider.OwnershipContext{
		ExpectedBuckets: map[string]bool{"nvoi-nvoi-production-db-main-backups": true},
	}
	groups := []provider.ResourceGroup{{
		Name:    "R2 Buckets",
		Columns: []string{"Name"},
		Rows: [][]string{
			{"nvoi-nvoi-production-db-main-backups"},    // owned (in cfg)
			{"nvoi-nvoi-production-bugsink-db-backups"}, // external (not in cfg)
			{"nvoi-other-app-prod-something"},           // external
			{"nvoi-releases"},                           // external
			{"fastmag-production-backend"},              // external
		},
	}}
	Classify(groups, ctx)

	want := []provider.Scope{
		provider.ScopeOwned,
		provider.ScopeExternal,
		provider.ScopeExternal,
		provider.ScopeExternal,
		provider.ScopeExternal,
	}
	for i := range want {
		if groups[0].Scope[i] != want[i] {
			t.Errorf("row[%d] (%s) = %q, want %q", i, groups[0].Rows[i][0], groups[0].Scope[i], want[i])
		}
	}
}

// TestClassify_DNSCfgMatch — DNS records compare on their FQDN
// (the "Domain" column).
func TestClassify_DNSCfgMatch(t *testing.T) {
	ctx := &provider.OwnershipContext{
		ExpectedDNS: map[string]bool{"api.example.com": true},
	}
	groups := []provider.ResourceGroup{{
		Name:    "DNS Records",
		Columns: []string{"Type", "Domain", "Target"},
		Rows: [][]string{
			{"A", "api.example.com", "1.2.3.4"},      // owned
			{"A", "manual.example.com", "5.6.7.8"},   // external
			{"CNAME", "api.example.com", "tunnel.x"}, // owned (same domain)
		},
	}}
	Classify(groups, ctx)
	want := []provider.Scope{provider.ScopeOwned, provider.ScopeExternal, provider.ScopeOwned}
	for i := range want {
		if groups[0].Scope[i] != want[i] {
			t.Errorf("row[%d] = %q, want %q", i, groups[0].Scope[i], want[i])
		}
	}
}

// TestClassify_NilContext_AllExternal — no cfg loaded → every row
// classifies as external (nothing is "owned" without a cfg to compare
// against).
func TestClassify_NilContext_AllExternal(t *testing.T) {
	groups := []provider.ResourceGroup{{
		Name:    "Servers",
		Columns: []string{"ID", "Name"},
		Rows: [][]string{
			{"1", "nvoi-myapp-prod-master"},
			{"2", "manual-server"},
		},
	}}
	Classify(groups, nil)
	for i, s := range groups[0].Scope {
		if s != provider.ScopeExternal {
			t.Errorf("row[%d] = %q with nil ctx, want external", i, s)
		}
	}
}

// TestClassify_UnknownGroupSkipped — groups outside the taxonomy get
// no Scope column.
func TestClassify_UnknownGroupSkipped(t *testing.T) {
	groups := []provider.ResourceGroup{{
		Name:    "Subnets",
		Columns: []string{"ID", "Name"},
		Rows:    [][]string{{"sub-1", "default"}},
	}}
	Classify(groups, &provider.OwnershipContext{})
	if groups[0].Scope != nil {
		t.Errorf("unknown group must skip Scope; got %v", groups[0].Scope)
	}
}
