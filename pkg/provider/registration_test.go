package provider_test

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"

	// Blank imports — mirrors cmd/cli/main.go. Each vendor package registers
	// all its kinds (infra/dns/storage/tunnel) via init().
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/ngrok"
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"
)

// TestAllProvidersRegistered verifies that every provider name core might
// receive can be resolved. If this test fails, a blank import is missing from
// cmd/cli/main.go (or the provider's init() broke).
func TestAllProvidersRegistered(t *testing.T) {
	// Credentials are intentionally invalid — we're testing registration, not auth.
	// ResolveX will fail on credential validation, not on "unknown provider".
	// So we check the error message: "unknown" means not registered, anything else is fine.

	infra := []string{"hetzner", "aws", "scaleway"}
	for _, name := range infra {
		_, err := provider.ResolveInfra(name, map[string]string{})
		if err != nil && contains(err.Error(), "unsupported") {
			t.Errorf("infra provider %q not registered", name)
		}
	}

	dns := []string{"cloudflare", "aws", "scaleway"}
	for _, name := range dns {
		_, err := provider.ResolveDNS(name, map[string]string{})
		if err != nil && contains(err.Error(), "unsupported") {
			t.Errorf("dns provider %q not registered", name)
		}
	}

	bucket := []string{"cloudflare", "aws"}
	for _, name := range bucket {
		_, err := provider.ResolveBucket(name, map[string]string{})
		if err != nil && contains(err.Error(), "unsupported") {
			t.Errorf("bucket provider %q not registered", name)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
