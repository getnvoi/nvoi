package reconcile

import (
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

const testHash = "20260417-143022"

func svcCfg(image string, build *config.BuildSpec, registries map[string]config.RegistryDef) *config.AppConfig {
	return &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: registries,
		Services: map[string]config.ServiceDef{
			"api": {Image: image, Build: build},
		},
	}
}

// Kamal-style: repo-only image + single registry → host inferred + hash appended.
func TestResolveImage_BuildRepoOnlySingleRegistry(t *testing.T) {
	cfg := svcCfg("deemx/nvoi-api",
		&config.BuildSpec{Context: "./"},
		map[string]config.RegistryDef{"docker.io": {Username: "u", Password: "p"}},
	)
	got, err := ResolveImage(cfg, "api", testHash)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "docker.io/deemx/nvoi-api:" + testHash
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// User-pinned tag: kept AND hash suffixed.
func TestResolveImage_BuildWithUserTag(t *testing.T) {
	cfg := svcCfg("deemx/nvoi-api:v2",
		&config.BuildSpec{Context: "./"},
		map[string]config.RegistryDef{"docker.io": {Username: "u", Password: "p"}},
	)
	got, err := ResolveImage(cfg, "api", testHash)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "docker.io/deemx/nvoi-api:v2-" + testHash
	if got != want {
		t.Errorf("got %q, want %q (user tag + - + hash)", got, want)
	}
}

// Host-qualified image + user tag → host kept, tag-hash suffix applied.
func TestResolveImage_BuildHostQualifiedWithTag(t *testing.T) {
	cfg := svcCfg("ghcr.io/org/api:v2",
		&config.BuildSpec{Context: "./"},
		map[string]config.RegistryDef{"ghcr.io": {Username: "u", Password: "p"}},
	)
	got, err := ResolveImage(cfg, "api", testHash)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "ghcr.io/org/api:v2-" + testHash
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Host-qualified, no user tag → just the hash.
func TestResolveImage_BuildHostQualifiedNoTag(t *testing.T) {
	cfg := svcCfg("ghcr.io/org/api",
		&config.BuildSpec{Context: "./"},
		map[string]config.RegistryDef{"ghcr.io": {Username: "u", Password: "p"}},
	)
	got, err := ResolveImage(cfg, "api", testHash)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "ghcr.io/org/api:" + testHash
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Pull-only (no build): image passes through verbatim, even without a
// `registry:` block.
func TestResolveImage_PullOnlyPassesThrough(t *testing.T) {
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Services: map[string]config.ServiceDef{
			"db": {Image: "postgres:17"},
		},
	}
	got, err := ResolveImage(cfg, "db", testHash)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "postgres:17" {
		t.Errorf("pull-only must pass through, got %q", got)
	}
}

// Ambiguity: repo-only image with multiple registries declared → error.
func TestResolveImage_BuildAmbiguous(t *testing.T) {
	cfg := svcCfg("org/api",
		&config.BuildSpec{Context: "./"},
		map[string]config.RegistryDef{
			"docker.io": {Username: "u", Password: "p"},
			"ghcr.io":   {Username: "u", Password: "p"},
		},
	)
	_, err := ResolveImage(cfg, "api", testHash)
	if err == nil {
		t.Fatal("expected error for ambiguous registry")
	}
	if !strings.Contains(err.Error(), "multiple registries") {
		t.Errorf("error should mention multiple registries, got: %v", err)
	}
}

// Host explicit but NOT declared in registry: → error.
func TestResolveImage_BuildHostNotDeclared(t *testing.T) {
	cfg := svcCfg("ghcr.io/org/api",
		&config.BuildSpec{Context: "./"},
		map[string]config.RegistryDef{"docker.io": {Username: "u", Password: "p"}},
	)
	_, err := ResolveImage(cfg, "api", testHash)
	if err == nil {
		t.Fatal("expected error when image host isn't in registry: block")
	}
}

// Empty hash when build is set → programmer error, must surface loudly.
func TestResolveImage_BuildEmptyHash(t *testing.T) {
	cfg := svcCfg("org/api",
		&config.BuildSpec{Context: "./"},
		map[string]config.RegistryDef{"ghcr.io": {Username: "u", Password: "p"}},
	)
	_, err := ResolveImage(cfg, "api", "")
	if err == nil {
		t.Fatal("expected error for empty hash")
	}
	if !strings.Contains(err.Error(), "deploy hash") {
		t.Errorf("error should mention deploy hash, got: %v", err)
	}
}

// Digest-pinned images (sha256:...) do NOT get a hash suffix — the user
// intentionally pinned content. Respect it.
func TestResolveImage_DigestPinnedPassesThroughWithHostOnly(t *testing.T) {
	cfg := svcCfg("ghcr.io/org/api@sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		&config.BuildSpec{Context: "./"},
		map[string]config.RegistryDef{"ghcr.io": {Username: "u", Password: "p"}},
	)
	got, err := ResolveImage(cfg, "api", testHash)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Contains(got, testHash) {
		t.Errorf("digest-pinned image must not get hash suffix, got %q", got)
	}
}

// imageTag unit table — subtle host:port vs tag disambiguation.
func TestImageTag(t *testing.T) {
	tests := []struct{ in, want string }{
		{"foo", ""},
		{"foo:v1", "v1"},
		{"ghcr.io/org/foo", ""},
		{"ghcr.io/org/foo:v1", "v1"},
		{"localhost:5000/foo", ""},
		{"localhost:5000/foo:v1", "v1"},
		{"repo@sha256:abc", ""},
	}
	for _, tt := range tests {
		if got := imageTag(tt.in); got != tt.want {
			t.Errorf("imageTag(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
