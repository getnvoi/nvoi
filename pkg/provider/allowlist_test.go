package provider

import (
	"context"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseRawRules(t *testing.T) {
	got := ParseRawRules([]string{"80:0.0.0.0/0", "443:10.0.0.0/8,192.168.1.0/24"})
	want := PortAllowList{
		"80":  {"0.0.0.0/0"},
		"443": {"10.0.0.0/8", "192.168.1.0/24"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseRawRules (-want +got):\n%s", diff)
	}
}

func TestParseRawRules_EnvVar(t *testing.T) {
	// Semicolon-separated format used in NVOI_FIREWALL env var
	got := ParseRawRules([]string{"80:0.0.0.0/0;443:0.0.0.0/0"})
	want := PortAllowList{
		"80":  {"0.0.0.0/0"},
		"443": {"0.0.0.0/0"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseRawRules (-want +got):\n%s", diff)
	}
}

func TestParseRawRules_BareIPs(t *testing.T) {
	got := ParseRawRules([]string{"22:1.2.3.4"})
	want := PortAllowList{
		"22": {"1.2.3.4/32"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("bare IP not normalized to /32 (-want +got):\n%s", diff)
	}
}

func TestParseRawRules_Empty(t *testing.T) {
	got := ParseRawRules(nil)
	if got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
	got = ParseRawRules([]string{})
	if got != nil {
		t.Errorf("expected nil for empty slice, got %v", got)
	}
}

func TestResolveFirewallArgs_PresetDefault(t *testing.T) {
	got, err := ResolveFirewallArgs(context.Background(), []string{"default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got["80"]) == 0 || len(got["443"]) == 0 {
		t.Errorf("default preset should open 80 and 443, got %v", got)
	}
	if got["80"][0] != "0.0.0.0/0" {
		t.Errorf("80 should be open, got %v", got["80"])
	}
	if !containsCIDR(got["80"], "::/0") || !containsCIDR(got["443"], "::/0") {
		t.Errorf("default preset should be dual-stack, got %v", got)
	}
	// SSH should NOT be in the preset — managed by instance set
	if _, ok := got["22"]; ok {
		t.Errorf("SSH (22) should not be in preset, got %v", got["22"])
	}
}

func TestResolveFirewallArgs_PresetPlusOverride(t *testing.T) {
	got, err := ResolveFirewallArgs(context.Background(), []string{"default", "443:10.0.0.0/8"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 80 from preset
	if got["80"][0] != "0.0.0.0/0" {
		t.Errorf("80 should be from preset, got %v", got["80"])
	}
	// 443 overridden
	if got["443"][0] != "10.0.0.0/8" {
		t.Errorf("443 should be overridden, got %v", got["443"])
	}
}

func TestResolveFirewallArgs_UnknownPreset(t *testing.T) {
	_, err := ResolveFirewallArgs(context.Background(), []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown preset")
	}
	if !contains(err.Error(), "unknown firewall preset") {
		t.Errorf("error should mention unknown preset, got: %v", err)
	}
}

func TestResolveFirewallArgs_RawOnly(t *testing.T) {
	got, err := ResolveFirewallArgs(context.Background(), []string{"80:0.0.0.0/0", "443:0.0.0.0/0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 ports, got %d", len(got))
	}
}

func TestResolveFirewallArgs_Empty(t *testing.T) {
	got, err := ResolveFirewallArgs(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty args, got %v", got)
	}
}

func TestMergeAllowLists(t *testing.T) {
	base := PortAllowList{"80": {"1.2.3.4/32"}, "443": {"5.6.7.8/32"}}
	overrides := PortAllowList{"443": {"0.0.0.0/0"}, "8080": {"10.0.0.0/8"}}

	got := MergeAllowLists(base, overrides)

	// 80 from base
	if diff := cmp.Diff([]string{"1.2.3.4/32"}, got["80"]); diff != "" {
		t.Errorf("80 should be from base (-want +got):\n%s", diff)
	}
	// 443 overridden
	if diff := cmp.Diff([]string{"0.0.0.0/0"}, got["443"]); diff != "" {
		t.Errorf("443 should be overridden (-want +got):\n%s", diff)
	}
	// 8080 added
	if diff := cmp.Diff([]string{"10.0.0.0/8"}, got["8080"]); diff != "" {
		t.Errorf("8080 should be added (-want +got):\n%s", diff)
	}
}

func TestMergeAllowLists_BothNil(t *testing.T) {
	got := MergeAllowLists(nil, nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestParseRawRules_MultipleCIDRsPerPort(t *testing.T) {
	got := ParseRawRules([]string{"80:1.2.3.4,5.6.7.8/32,10.0.0.0/8"})
	if len(got["80"]) != 3 {
		t.Errorf("expected 3 CIDRs for port 80, got %d: %v", len(got["80"]), got["80"])
	}
	sort.Strings(got["80"])
	want := []string{"1.2.3.4/32", "10.0.0.0/8", "5.6.7.8/32"}
	if diff := cmp.Diff(want, got["80"]); diff != "" {
		t.Errorf("ParseRawRules multiple CIDRs (-want +got):\n%s", diff)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func containsCIDR(cidrs []string, want string) bool {
	for _, cidr := range cidrs {
		if cidr == want {
			return true
		}
	}
	return false
}
