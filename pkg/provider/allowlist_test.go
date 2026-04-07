package provider

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseRawRules(t *testing.T) {
	got, err := ParseRawRules([]string{"80:0.0.0.0/0", "443:10.0.0.0/8,192.168.1.0/24"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := PortAllowList{
		"80":  {"0.0.0.0/0"},
		"443": {"10.0.0.0/8", "192.168.1.0/24"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseRawRules = %v, want %v", got, want)
	}
}

func TestParseRawRules_EnvVar(t *testing.T) {
	// Semicolon-separated format used in NVOI_FIREWALL env var
	got, err := ParseRawRules([]string{"80:0.0.0.0/0;443:0.0.0.0/0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := PortAllowList{
		"80":  {"0.0.0.0/0"},
		"443": {"0.0.0.0/0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseRawRules = %v, want %v", got, want)
	}
}

func TestParseRawRules_BareIPs(t *testing.T) {
	got, err := ParseRawRules([]string{"22:1.2.3.4"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := PortAllowList{
		"22": {"1.2.3.4/32"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bare IP not normalized to /32: got %v, want %v", got, want)
	}
}

func TestParseRawRules_InvalidCIDR(t *testing.T) {
	_, err := ParseRawRules([]string{"80:999.999.999.999"})
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
	if !strings.Contains(err.Error(), "invalid CIDR") {
		t.Errorf("error should mention invalid CIDR, got: %v", err)
	}
}

func TestParseRawRules_InvalidPort(t *testing.T) {
	_, err := ParseRawRules([]string{"99999:0.0.0.0/0"})
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
	if !strings.Contains(err.Error(), "invalid port") {
		t.Errorf("error should mention invalid port, got: %v", err)
	}
}

func TestParseRawRules_Deduplication(t *testing.T) {
	got, err := ParseRawRules([]string{"80:1.2.3.4/32,1.2.3.4/32"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got["80"]) != 1 {
		t.Errorf("expected 1 CIDR after dedup, got %d: %v", len(got["80"]), got["80"])
	}
}

func TestParseRawRules_Empty(t *testing.T) {
	got, err := ParseRawRules(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
	got, err = ParseRawRules([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	// SSH should NOT be in the preset — managed by instance set
	if _, ok := got["22"]; ok {
		t.Errorf("SSH (22) should not be in preset, got %v", got["22"])
	}
}

func TestResolveFirewallArgs_PresetCloudflare(t *testing.T) {
	got, err := ResolveFirewallArgs(context.Background(), []string{"cloudflare"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got["80"]) == 0 || len(got["443"]) == 0 {
		t.Errorf("cloudflare preset should have 80 and 443, got %v", got)
	}
	// Should have Cloudflare IPs (either live or fallback — includes IPv4 + IPv6)
	if len(got["80"]) < 2 {
		t.Errorf("cloudflare preset should have multiple IPs, got %v", got["80"])
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
	if !strings.Contains(err.Error(), "unknown firewall preset") {
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
	if !reflect.DeepEqual(got["80"], []string{"1.2.3.4/32"}) {
		t.Errorf("80 should be from base, got %v", got["80"])
	}
	// 443 overridden
	if !reflect.DeepEqual(got["443"], []string{"0.0.0.0/0"}) {
		t.Errorf("443 should be overridden, got %v", got["443"])
	}
	// 8080 added
	if !reflect.DeepEqual(got["8080"], []string{"10.0.0.0/8"}) {
		t.Errorf("8080 should be added, got %v", got["8080"])
	}
}

func TestMergeAllowLists_BothNil(t *testing.T) {
	got := MergeAllowLists(nil, nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestFallbackCloudflareIPs(t *testing.T) {
	if len(FallbackCloudflareIPs) < 10 {
		t.Errorf("expected at least 10 fallback IPs, got %d", len(FallbackCloudflareIPs))
	}
	// Verify all are valid CIDRs (contain /)
	for _, cidr := range FallbackCloudflareIPs {
		if !strings.Contains(cidr, "/") {
			t.Errorf("fallback IP %q is not a CIDR", cidr)
		}
	}
}

func TestParseRawRules_MultipleCIDRsPerPort(t *testing.T) {
	got, err := ParseRawRules([]string{"80:1.2.3.4,5.6.7.8/32,10.0.0.0/8"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got["80"]) != 3 {
		t.Errorf("expected 3 CIDRs for port 80, got %d: %v", len(got["80"]), got["80"])
	}
	sort.Strings(got["80"])
	want := []string{"1.2.3.4/32", "10.0.0.0/8", "5.6.7.8/32"}
	if !reflect.DeepEqual(got["80"], want) {
		t.Errorf("got %v, want %v", got["80"], want)
	}
}
