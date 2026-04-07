package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
)

func TestParseRawRules(t *testing.T) {
	got := ParseRawRules([]string{"80:0.0.0.0/0", "443:10.0.0.0/8,192.168.1.0/24"})
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
	got := ParseRawRules([]string{"80:0.0.0.0/0;443:0.0.0.0/0"})
	want := PortAllowList{
		"80":  {"0.0.0.0/0"},
		"443": {"0.0.0.0/0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseRawRules = %v, want %v", got, want)
	}
}

func TestParseRawRules_BareIPs(t *testing.T) {
	got := ParseRawRules([]string{"22:1.2.3.4"})
	want := PortAllowList{
		"22": {"1.2.3.4/32"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bare IP not normalized to /32: got %v, want %v", got, want)
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

func TestResolveFirewallArgs_PresetCloudflare(t *testing.T) {
	// Mock the Cloudflare API
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"ipv4_cidrs": []string{"173.245.48.0/20", "103.21.244.0/22"},
			},
		})
	}))
	defer ts.Close()

	// Can't easily override the URL in the current API, so test with fallback
	got, err := ResolveFirewallArgs(context.Background(), []string{"cloudflare"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got["80"]) == 0 || len(got["443"]) == 0 {
		t.Errorf("cloudflare preset should have 80 and 443, got %v", got)
	}
	// Should have Cloudflare IPs (either live or fallback)
	if len(got["80"]) < 2 {
		t.Errorf("cloudflare preset should have multiple IPs, got %v", got["80"])
	}
	if !hasIPv6CIDR(got["80"]) || !hasIPv6CIDR(got["443"]) {
		t.Errorf("cloudflare preset should include IPv6 CIDRs, got %v", got)
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
		if !containsStr(cidr, "/") {
			t.Errorf("fallback IP %q is not a CIDR", cidr)
		}
	}
	if !hasIPv6CIDR(FallbackCloudflareIPs) {
		t.Errorf("fallback Cloudflare CIDRs should include IPv6, got %v", FallbackCloudflareIPs)
	}
}

func TestParseRawRules_MultipleCIDRsPerPort(t *testing.T) {
	got := ParseRawRules([]string{"80:1.2.3.4,5.6.7.8/32,10.0.0.0/8"})
	if len(got["80"]) != 3 {
		t.Errorf("expected 3 CIDRs for port 80, got %d: %v", len(got["80"]), got["80"])
	}
	sort.Strings(got["80"])
	want := []string{"1.2.3.4/32", "10.0.0.0/8", "5.6.7.8/32"}
	if !reflect.DeepEqual(got["80"], want) {
		t.Errorf("got %v, want %v", got["80"], want)
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

func hasIPv6CIDR(cidrs []string) bool {
	for _, cidr := range cidrs {
		if containsStr(cidr, ":") {
			return true
		}
	}
	return false
}
