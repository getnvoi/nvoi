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
	// Path exists, but PEM in source should win.
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
	// Put a disk key AND a source path; source path should win.
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

// EnvSource backs env var lookups — verify it works identically for the no-provider path.
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

// ── Strict mode: secrets provider enabled → no disk/subprocess fallbacks ────

// fakeSecretsProvider lets us construct a provider.SecretsSource without hitting a real API.
type fakeSecretsProvider struct {
	m map[string]string
}

func (f *fakeSecretsProvider) ValidateCredentials(ctx context.Context) error { return nil }
func (f *fakeSecretsProvider) Get(ctx context.Context, key string) (string, error) {
	return f.m[key], nil
}
func (f *fakeSecretsProvider) List(ctx context.Context) ([]string, error) { return nil, nil }

func secretsSourceWith(m map[string]string) provider.SecretsSource {
	return provider.SecretsSource{Ctx: context.Background(), Provider: &fakeSecretsProvider{m: m}}
}

func TestResolveSSHKey_Strict_PEMPresent(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // disk fallback would otherwise be blank anyway
	src := secretsSourceWith(map[string]string{"SSH_PRIVATE_KEY": "strict-pem"})

	got, err := resolveSSHKey(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "strict-pem" {
		t.Errorf("got %q, want strict-pem", string(got))
	}
}

func TestResolveSSHKey_Strict_MissingPEM_Errors(t *testing.T) {
	// DISK HAS A KEY — strict mode must ignore it.
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("disk-key"), 0o600)

	src := secretsSourceWith(map[string]string{}) // provider has nothing

	_, err := resolveSSHKey(src)
	if err == nil {
		t.Fatal("strict mode must hard-error when SSH_PRIVATE_KEY missing, ignoring disk fallback")
	}
	if !strings.Contains(err.Error(), "strict") {
		t.Errorf("error = %q, should mention strict mode", err.Error())
	}
}

func TestResolveSSHKey_Strict_IgnoresSSHKeyPath(t *testing.T) {
	// Provider exposes SSH_KEY_PATH pointing at a real file. Strict mode still rejects —
	// only SSH_PRIVATE_KEY (content) is accepted.
	dir := t.TempDir()
	path := filepath.Join(dir, "custom")
	os.WriteFile(path, []byte("pathed-key"), 0o600)

	src := secretsSourceWith(map[string]string{"SSH_KEY_PATH": path})

	_, err := resolveSSHKey(src)
	if err == nil {
		t.Fatal("strict mode must hard-error; SSH_KEY_PATH indirection is not accepted")
	}
}

// ── resolveGitAuth ───────────────────────────────────────────────────────────

func TestResolveGitAuth_FromSource(t *testing.T) {
	src := provider.MapSource{M: map[string]string{"GITHUB_TOKEN": "ghp_abc123"}}
	user, token := resolveGitAuth(src)
	if user != "x-access-token" {
		t.Errorf("user = %q, want x-access-token", user)
	}
	if token != "ghp_abc123" {
		t.Errorf("token = %q, want ghp_abc123", token)
	}
}

func TestResolveGitAuth_EmptySourceAndNoGh(t *testing.T) {
	// No GITHUB_TOKEN anywhere. If `gh` is installed on this machine, it may
	// return a token — so we can't assert empty return absolutely. We only
	// assert that the source path is checked first when present.
	src := provider.MapSource{M: map[string]string{"GITHUB_TOKEN": ""}}
	user, token := resolveGitAuth(src)
	// If gh returned a value, user will be "x-access-token" — that's the subprocess fallback,
	// correctly exercised. If gh is absent or unauthenticated, both are empty.
	if user != "" && user != "x-access-token" {
		t.Errorf("unexpected user = %q", user)
	}
	if user == "" && token != "" {
		t.Error("inconsistent: empty user but non-empty token")
	}
}

func TestResolveGitAuth_EnvSourceMode(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env_token")
	user, token := resolveGitAuth(provider.EnvSource{})
	if user != "x-access-token" || token != "env_token" {
		t.Errorf("EnvSource should surface env var; got user=%q token=%q", user, token)
	}
}

func TestResolveGitAuth_Strict_TokenPresent(t *testing.T) {
	src := secretsSourceWith(map[string]string{"GITHUB_TOKEN": "strict_tok"})
	user, token := resolveGitAuth(src)
	if user != "x-access-token" || token != "strict_tok" {
		t.Errorf("got (%q,%q), want (x-access-token, strict_tok)", user, token)
	}
}

func TestResolveGitAuth_Strict_NoSubprocessFallback(t *testing.T) {
	// If `gh` is installed on this machine, the env-mode path would return a token.
	// Strict mode must NOT call it. We assert empty return even when gh would succeed.
	src := secretsSourceWith(map[string]string{})
	user, token := resolveGitAuth(src)
	if user != "" || token != "" {
		t.Errorf("strict mode must not fall back to gh subprocess; got (%q,%q)", user, token)
	}
}

// ── resolveDatabaseCreds ─────────────────────────────────────────────────────

func TestResolveDatabaseCreds_Empty(t *testing.T) {
	cfg := &config.AppConfig{}
	if got := resolveDatabaseCreds(provider.MapSource{}, cfg); got != nil {
		t.Errorf("no databases → nil, got %+v", got)
	}
}

func TestResolveDatabaseCreds_FromSource(t *testing.T) {
	cfg := &config.AppConfig{
		Database: map[string]config.DatabaseDef{
			"main": {Kind: "postgres"},
		},
	}
	src := provider.MapSource{M: map[string]string{
		"MAIN_POSTGRES_USER":     "u",
		"MAIN_POSTGRES_PASSWORD": "p",
		"MAIN_POSTGRES_DB":       "d",
	}}
	got := resolveDatabaseCreds(src, cfg)
	if got["main"] == nil {
		t.Fatal("main db creds missing")
	}
	if got["main"].User != "u" || got["main"].Password != "p" || got["main"].DBName != "d" {
		t.Errorf("unexpected creds: %+v", got["main"])
	}
}

func TestResolveDatabaseCreds_MultipleDatabases(t *testing.T) {
	cfg := &config.AppConfig{
		Database: map[string]config.DatabaseDef{
			"main":    {Kind: "postgres"},
			"bugsink": {Kind: "postgres"},
		},
	}
	src := provider.MapSource{M: map[string]string{
		"MAIN_POSTGRES_USER":        "mu",
		"MAIN_POSTGRES_PASSWORD":    "mp",
		"MAIN_POSTGRES_DB":          "md",
		"BUGSINK_POSTGRES_USER":     "bu",
		"BUGSINK_POSTGRES_PASSWORD": "bp",
		"BUGSINK_POSTGRES_DB":       "bd",
	}}
	got := resolveDatabaseCreds(src, cfg)
	if got["main"].User != "mu" || got["bugsink"].User != "bu" {
		t.Errorf("per-db prefixing broken: %+v", got)
	}
}

// ── credentialSource ─────────────────────────────────────────────────────────

func TestCredentialSource_NoSecretsProvider_ReturnsEnvSource(t *testing.T) {
	cfg := &config.AppConfig{}
	src, err := credentialSource(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := src.(provider.EnvSource); !ok {
		t.Errorf("no provider → want EnvSource, got %T", src)
	}
}

func TestCredentialSource_ConfiguredButNoBootstrapCreds_Errors(t *testing.T) {
	// Clear env so secrets-provider bootstrap finds nothing.
	t.Setenv("INFISICAL_CLIENT_ID", "")
	t.Setenv("INFISICAL_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_PROJECT_SLUG", "")

	cfg := &config.AppConfig{Providers: config.ProvidersDef{Secrets: "infisical"}}
	_, err := credentialSource(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected hard error when secrets provider configured but no bootstrap creds")
	}
	if !strings.Contains(err.Error(), "bootstrap") {
		t.Errorf("error = %q, want mention of bootstrap", err.Error())
	}
}

func TestCredentialSource_UnknownProvider_Errors(t *testing.T) {
	cfg := &config.AppConfig{Providers: config.ProvidersDef{Secrets: "nosuchprovider"}}
	_, err := credentialSource(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for unknown secrets provider")
	}
}

func TestCredentialSource_InfisicalBootstrap_ReturnsSecretsSource(t *testing.T) {
	// Bootstrap Infisical from env — doesn't actually call the API because
	// SecretsSource lazily authenticates on first Get. Here we only verify
	// that the right source type is returned.
	t.Setenv("INFISICAL_CLIENT_ID", "id")
	t.Setenv("INFISICAL_CLIENT_SECRET", "secret")
	t.Setenv("INFISICAL_PROJECT_SLUG", "proj")

	cfg := &config.AppConfig{Providers: config.ProvidersDef{Secrets: "infisical"}}
	src, err := credentialSource(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := src.(provider.SecretsSource); !ok {
		t.Errorf("configured infisical → want SecretsSource, got %T", src)
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
	// No providers.secrets — EnvSource-only path. Env vars populate the context.
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
		t.Errorf("dc.Creds should be EnvSource in env-only mode, got %T", dc.Creds)
	}
}

func TestBuildDeployContext_SecretsProviderMisconfigured_Errors(t *testing.T) {
	// Clear any bootstrap creds that could be hanging around from other tests.
	t.Setenv("INFISICAL_CLIENT_ID", "")
	t.Setenv("INFISICAL_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_PROJECT_SLUG", "")

	cfg := &config.AppConfig{
		App: "app", Env: "prod",
		Providers: config.ProvidersDef{Compute: "hetzner", Secrets: "infisical"},
	}
	_, err := buildDeployContext(context.Background(), nil, cfg)
	if err == nil {
		t.Fatal("expected hard error; the secrets provider must fail loudly, not silently fall back")
	}
}
