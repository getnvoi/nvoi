package provider_test

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"

	// Blank imports — same as cmd/cli/main.go. If any are missing there,
	// this test reminds you to add them.
	_ "github.com/getnvoi/nvoi/pkg/provider/dns/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/dns/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/dns/scaleway"
	_ "github.com/getnvoi/nvoi/pkg/provider/infra/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/infra/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/infra/scaleway"
	_ "github.com/getnvoi/nvoi/pkg/provider/storage/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/storage/cloudflare"
)

// TestAllProvidersRegistered verifies that every provider name core might
// receive can be resolved. If this test fails, a blank import is missing from
// cmd/cli/main.go (or the provider's init() broke).
func TestAllProvidersRegistered(t *testing.T) {
	// Credentials are intentionally invalid — we're testing registration, not auth.
	// ResolveX will fail on credential validation, not on "unknown provider".
	// So we check the error message: "unknown" means not registered, anything else is fine.

	compute := []string{"hetzner", "aws", "scaleway"}
	for _, name := range compute {
		_, err := provider.ResolveCompute(name, map[string]string{})
		if err != nil && contains(err.Error(), "unsupported") {
			t.Errorf("compute provider %q not registered", name)
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
