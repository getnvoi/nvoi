package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ── resolveSSHKey ────────────────────────────────────────────────────────────

func TestResolveSSHKey_FromSource_PEM(t *testing.T) {
	// HOME points at a temp dir with no .ssh/ so filesystem fallback can't interfere.
	t.Setenv("HOME", t.TempDir())

	pem := "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA...\n-----END OPENSSH PRIVATE KEY-----"
	src := provider.MapSource{M: map[string]string{"SSH_PRIVATE_KEY": pem}}

	got, err := resolveSSHKey(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != pem {
		t.Errorf("got %q, want PEM blob verbatim", string(got))
	}
}

func TestResolveSSHKey_FromSource_AbsolutePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "custom_key")
	if err := os.WriteFile(path, []byte("file-backed-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := provider.MapSource{M: map[string]string{"SSH_KEY_PATH": path}}

	got, err := resolveSSHKey(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "file-backed-key" {
		t.Errorf("got %q, want file-backed-key", string(got))
	}
}

func TestResolveSSHKey_FromSource_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "my-key")
	if err := os.WriteFile(path, []byte("tilde-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := provider.MapSource{M: map[string]string{"SSH_KEY_PATH": "~/my-key"}}

	got, err := resolveSSHKey(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "tilde-key" {
		t.Errorf("got %q, want tilde-key", string(got))
	}
}

func TestResolveSSHKey_FromDisk_Ed25519(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("ed25519-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveSSHKey(provider.MapSource{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "ed25519-key" {
		t.Errorf("got %q, want ed25519-key", string(got))
	}
}

func TestResolveSSHKey_FromDisk_RsaFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Only id_rsa, no id_ed25519 — exercise the fallback order.
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte("rsa-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveSSHKey(provider.MapSource{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "rsa-key" {
		t.Errorf("got %q, want rsa-key", string(got))
	}
}

func TestResolveSSHKey_NoneAnywhere(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // empty HOME, no .ssh

	_, err := resolveSSHKey(provider.MapSource{})
	if err == nil {
		t.Fatal("expected error when no key is available anywhere")
	}
	if !strings.Contains(err.Error(), "no SSH key found") {
		t.Errorf("error = %q, want 'no SSH key found'", err.Error())
	}
}

func TestResolveSSHKey_PEMBeatsPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "from-path")
	os.WriteFile(path, []byte("from-path"), 0o600)

	src := provider.MapSource{M: map[string]string{
		"SSH_PRIVATE_KEY": "from-pem",
		"SSH_KEY_PATH":    path,
	}}

	got, err := resolveSSHKey(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "from-pem" {
		t.Errorf("PEM in source must win over SSH_KEY_PATH; got %q", string(got))
	}
}

func TestResolveSSHKey_PathBeatsDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("disk-key"), 0o600)

	dir := t.TempDir()
	path := filepath.Join(dir, "custom")
	os.WriteFile(path, []byte("path-key"), 0o600)

	src := provider.MapSource{M: map[string]string{"SSH_KEY_PATH": path}}
	got, err := resolveSSHKey(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "path-key" {
		t.Errorf("SSH_KEY_PATH must win over disk fallback; got %q", string(got))
	}
}

func TestResolveSSHKey_InvalidPathReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := provider.MapSource{M: map[string]string{"SSH_KEY_PATH": "/nonexistent/nope"}}

	_, err := resolveSSHKey(src)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// EnvSource backs env var lookups — verify it works identically for the default path.
func TestResolveSSHKey_EnvSourceMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "ci-key")
	os.WriteFile(path, []byte("ci-key"), 0o600)
	t.Setenv("SSH_KEY_PATH", path)

	got, err := resolveSSHKey(provider.EnvSource{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "ci-key" {
		t.Errorf("got %q, want ci-key (via EnvSource)", string(got))
	}
}

// ── resolveProviderCreds ─────────────────────────────────────────────────────

func TestResolveProviderCreds_EmptyName(t *testing.T) {
	got, err := resolveProviderCreds(provider.MapSource{}, "compute", "")
	if err != nil {
		t.Fatalf("empty name must not error: %v", err)
	}
	if got != nil {
		t.Errorf("empty name → nil map, got %+v", got)
	}
}

func TestResolveProviderCreds_UnknownProvider(t *testing.T) {
	_, err := resolveProviderCreds(provider.MapSource{}, "compute", "nosuch")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

// ── buildDeployContext end-to-end ────────────────────────────────────────────

func TestBuildDeployContext_EnvOnlyMode(t *testing.T) {
	t.Setenv("HETZNER_TOKEN", "htok")
	t.Setenv("CF_API_KEY", "cfkey")
	t.Setenv("CF_ZONE_ID", "zone")
	t.Setenv("DNS_ZONE", "example.com")
	t.Setenv("CF_ACCOUNT_ID", "acct")
	// SSH key via env path
	home := t.TempDir()
	t.Setenv("HOME", home)
	keyPath := filepath.Join(home, "envkey")
	os.WriteFile(keyPath, []byte("env-ssh"), 0o600)
	t.Setenv("SSH_KEY_PATH", keyPath)

	cfg := &config.AppConfig{
		App: "app", Env: "prod",
		Providers: config.ProvidersDef{
			Compute: "hetzner", DNS: "cloudflare", Storage: "cloudflare",
		},
	}

	dc, err := buildDeployContext(context.Background(), nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dc.Cluster.Credentials["token"] != "htok" {
		t.Errorf("compute token = %q, want htok", dc.Cluster.Credentials["token"])
	}
	if dc.DNS.Creds["api_key"] != "cfkey" {
		t.Errorf("dns api_key = %q, want cfkey", dc.DNS.Creds["api_key"])
	}
	if string(dc.Cluster.SSHKey) != "env-ssh" {
		t.Errorf("SSH key should be env-backed, got %q", string(dc.Cluster.SSHKey))
	}
	// Creds field on DeployContext must be populated and be an EnvSource.
	if _, ok := dc.Creds.(provider.EnvSource); !ok {
		t.Errorf("dc.Creds should be EnvSource, got %T", dc.Creds)
	}
}
