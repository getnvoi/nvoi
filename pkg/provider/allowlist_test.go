package provider

import (
	"context"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// stubView is a minimal ProviderConfigView for FirewallAllowList tests.
// ProviderConfigView is a data-view interface (not a provider interface), so
// a local stub here is explicitly permitted by the mock governance rules.
type stubView struct {
	domains map[string][]string
	tunnel  string
	rules   []string
}

func (s *stubView) AppName() string                       { return "test" }
func (s *stubView) EnvName() string                       { return "dev" }
func (s *stubView) ServerDefs() []ServerSpec              { return nil }
func (s *stubView) FirewallRules() []string               { return s.rules }
func (s *stubView) VolumeDefs() []VolumeSpec              { return nil }
func (s *stubView) ServiceDefs() []ServiceSpec            { return nil }
func (s *stubView) DomainsByService() map[string][]string { return s.domains }
func (s *stubView) TunnelProvider() string                { return s.tunnel }

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

// Presets were removed — any plain word (no ":") is a hard error.
func TestResolveFirewallArgs_PresetLikeArg_Errors(t *testing.T) {
	for _, arg := range []string{"default", "cloudflare", "something"} {
		_, err := ResolveFirewallArgs(context.Background(), []string{arg})
		if err == nil {
			t.Errorf("expected error for preset-like arg %q, got nil", arg)
		}
		if !contains(err.Error(), "port:cidr") {
			t.Errorf("error for %q should hint at port:cidr format, got: %v", arg, err)
		}
	}
}

// ── FirewallAllowList ────────────────────────────────────────────────────────

func TestFirewallAllowList_CaddyMode_Opens80And443(t *testing.T) {
	cfg := &stubView{domains: map[string][]string{"api": {"api.example.com"}}, tunnel: ""}
	rules, err := FirewallAllowList(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules["80"]) == 0 {
		t.Error("Caddy mode: port 80 should be open")
	}
	if len(rules["443"]) == 0 {
		t.Error("Caddy mode: port 443 should be open")
	}
	if !containsCIDR(rules["80"], "0.0.0.0/0") || !containsCIDR(rules["80"], "::/0") {
		t.Errorf("port 80 should be dual-stack, got %v", rules["80"])
	}
}

func TestFirewallAllowList_TunnelMode_NoHTTPPorts(t *testing.T) {
	cfg := &stubView{
		domains: map[string][]string{"api": {"api.example.com"}},
		tunnel:  "cloudflare",
	}
	rules, err := FirewallAllowList(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := rules["80"]; ok {
		t.Error("tunnel mode: port 80 must be closed")
	}
	if _, ok := rules["443"]; ok {
		t.Error("tunnel mode: port 443 must be closed")
	}
}

func TestFirewallAllowList_NoDomains_NoHTTPPorts(t *testing.T) {
	cfg := &stubView{domains: nil, tunnel: ""}
	rules, err := FirewallAllowList(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := rules["80"]; ok {
		t.Error("no domains: port 80 must be closed")
	}
	if _, ok := rules["443"]; ok {
		t.Error("no domains: port 443 must be closed")
	}
}

func TestFirewallAllowList_UserOverride_MergedOnTop(t *testing.T) {
	// User restricts SSH to a specific CIDR in Caddy mode.
	cfg := &stubView{
		domains: map[string][]string{"api": {"api.example.com"}},
		tunnel:  "",
		rules:   []string{"22:10.0.0.1/32"},
	}
	rules, err := FirewallAllowList(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 80/443 still auto-derived
	if len(rules["80"]) == 0 || len(rules["443"]) == 0 {
		t.Error("80/443 should still be auto-open in Caddy mode even with SSH override")
	}
	// SSH CIDR overridden
	if len(rules["22"]) != 1 || rules["22"][0] != "10.0.0.1/32" {
		t.Errorf("SSH override not applied, got %v", rules["22"])
	}
}

func TestFirewallAllowList_InvalidRule_Errors(t *testing.T) {
	cfg := &stubView{rules: []string{"default"}}
	_, err := FirewallAllowList(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for invalid (preset-like) rule")
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
