package reconcile

import (
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func dnsDC(ssh *testutil.MockSSH) *config.DeployContext {
	sshKey, _, _ := utils.GenerateEd25519Key()
	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-compute", Credentials: map[string]string{},
			SSHKey:    sshKey,
			Output:    &testutil.MockOutput{},
			MasterSSH: ssh,
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return ssh, nil
			},
		},
	}
}

func TestVerifyDNSPropagation_ResolvesViaSSH(t *testing.T) {
	// getent on the server returns the master IP — should produce no warning.
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "getent hosts myapp.com", Result: testutil.MockResult{Output: []byte("1.2.3.4    myapp.com")}},
		},
	}
	dc := dnsDC(ssh)
	out := dc.Cluster.Output.(*testutil.MockOutput)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Compute: "test-compute"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Domains:   map[string][]string{"web": {"myapp.com"}},
	}

	verifyDNSPropagation(context.Background(), dc, cfg)

	if len(out.Warnings) > 0 {
		t.Errorf("expected no warnings when DNS resolves correctly, got: %v", out.Warnings)
	}
	// Must have called getent via SSH, not net.LookupHost
	foundGetent := false
	for _, cmd := range ssh.Calls {
		if cmd == "getent hosts myapp.com 2>/dev/null" {
			foundGetent = true
		}
	}
	if !foundGetent {
		t.Errorf("DNS check must run via SSH (getent), not client-side. calls: %v", ssh.Calls)
	}
}

func TestVerifyDNSPropagation_WarnsWhenNotResolved(t *testing.T) {
	// getent fails — domain not resolved from the server's perspective.
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "getent hosts myapp.com", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
		},
	}
	dc := dnsDC(ssh)
	out := dc.Cluster.Output.(*testutil.MockOutput)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Compute: "test-compute"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Domains:   map[string][]string{"web": {"myapp.com"}},
	}

	verifyDNSPropagation(context.Background(), dc, cfg)

	if len(out.Warnings) == 0 {
		t.Error("expected warning when domain does not resolve")
	}
}

func TestVerifyDNSPropagation_WarnsWhenWrongIP(t *testing.T) {
	// getent returns a different IP — stale DNS cache on the server.
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "getent hosts myapp.com", Result: testutil.MockResult{Output: []byte("9.9.9.9    myapp.com")}},
		},
	}
	dc := dnsDC(ssh)
	out := dc.Cluster.Output.(*testutil.MockOutput)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Compute: "test-compute"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Domains:   map[string][]string{"web": {"myapp.com"}},
	}

	verifyDNSPropagation(context.Background(), dc, cfg)

	if len(out.Warnings) == 0 {
		t.Error("expected warning when domain resolves to wrong IP")
	}
}

func TestVerifyDNSPropagation_MultipleDomains(t *testing.T) {
	// Two domains: one resolves, one doesn't.
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "getent hosts myapp.com", Result: testutil.MockResult{Output: []byte("1.2.3.4    myapp.com")}},
			{Prefix: "getent hosts www.myapp.com", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
		},
	}
	dc := dnsDC(ssh)
	out := dc.Cluster.Output.(*testutil.MockOutput)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Compute: "test-compute"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Domains:   map[string][]string{"web": {"myapp.com", "www.myapp.com"}},
	}

	verifyDNSPropagation(context.Background(), dc, cfg)

	if len(out.Warnings) == 0 {
		t.Error("expected warning when one domain fails to resolve")
	}
	if len(out.Warnings) > 0 && !contains(out.Warnings[0], "1 domain") {
		t.Errorf("warning should mention 1 unresolved domain, got: %s", out.Warnings[0])
	}
}

func TestVerifyDNSPropagation_NoMasterSSH_Noop(t *testing.T) {
	dc := dnsDC(nil)
	dc.Cluster.MasterSSH = nil
	out := dc.Cluster.Output.(*testutil.MockOutput)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Compute: "test-compute"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Domains:   map[string][]string{"web": {"myapp.com"}},
	}

	// Should not panic or error — just skip.
	verifyDNSPropagation(context.Background(), dc, cfg)

	if len(out.Warnings) > 0 {
		t.Error("should not warn when no SSH connection available")
	}
}

func TestVerifyDNSPropagation_NoMasterServer_Noop(t *testing.T) {
	ssh := &testutil.MockSSH{}
	dc := dnsDC(ssh)
	// Register a provider that returns no servers — Master() will fail.
	provName := "test-dns-verify-empty"
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{Servers: nil}
	})
	dc.Cluster.Provider = provName
	out := dc.Cluster.Output.(*testutil.MockOutput)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Compute: provName},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Domains:   map[string][]string{"web": {"myapp.com"}},
	}

	verifyDNSPropagation(context.Background(), dc, cfg)

	if len(out.Warnings) > 0 {
		t.Error("should not warn when master server not found")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
