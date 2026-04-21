package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ciInitFixture wires a real GitHubCI against GitHubFake and returns the
// runtime + fake pair. Every test in this file starts from here.
func ciInitFixture(t *testing.T, cfg *config.AppConfig, creds map[string]string) (*runtime, *testutil.GitHubFake) {
	t.Helper()
	fake := testutil.NewGitHubFake(t)
	// Per-test provider name so subtests don't clash in the global registry.
	provName := "github-" + t.Name()
	fake.Register(provName)
	cfg.Providers.Ci = provName

	// MapSource gives us tight control over which env values the secret
	// collector sees. Setting creds with explicit keys documents what
	// the test expects to end up on the runner.
	source := provider.MapSource{M: creds}

	rt := &runtime{
		cfg: cfg,
		out: &testutil.MockOutput{},
		dc: &config.DeployContext{
			Cluster: app.Cluster{
				AppName:  cfg.App,
				Env:      cfg.Env,
				Provider: cfg.Providers.Infra,
				Output:   &testutil.MockOutput{},
			},
			Creds: source,
		},
	}
	return rt, fake
}

func minimalCfg() *config.AppConfig {
	return &config.AppConfig{
		App: "myapp",
		Env: "prod",
		Providers: config.ProvidersDef{
			Infra: "hetzner",
		},
		Servers: map[string]config.ServerDef{
			"master": {Type: "cx23", Region: "fsn1", Role: "master"},
		},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80},
		},
	}
}

func TestCIInit_DirectPush_SyncsSecretsAndCommits(t *testing.T) {
	cfg := minimalCfg()
	cfg.Secrets = []string{"JWT_SECRET", "DB_URL"}

	creds := map[string]string{
		"HETZNER_TOKEN":   "hz-token",
		"SSH_PRIVATE_KEY": "fake-pem",
		"JWT_SECRET":      "jwt-value",
		"DB_URL":          "postgres://...",
		"GITHUB_TOKEN":    "gh-token",
		"GITHUB_REPO":     "acme/api",
	}
	rt, fake := ciInitFixture(t, cfg, creds)
	fake.SeedRepo("acme", "api", "main")

	if err := runCIInit(context.Background(), rt, "v0.1.0"); err != nil {
		t.Fatalf("runCIInit: %v", err)
	}

	// Every listed secret landed on the repo with its expected plaintext.
	wantSecrets := map[string]string{
		"HETZNER_TOKEN":   "hz-token",
		"SSH_PRIVATE_KEY": "fake-pem",
		"JWT_SECRET":      "jwt-value",
		"DB_URL":          "postgres://...",
	}
	for k, want := range wantSecrets {
		got, ok := fake.SecretValue("acme", "api", k)
		if !ok {
			t.Errorf("secret %q was not synced; ops=%v", k, fake.All())
			continue
		}
		if got != want {
			t.Errorf("secret %q: want %q got %q", k, want, got)
		}
	}

	// Workflow committed directly to main (no PR fallback).
	content, ok := fake.FileContent("acme", "api", "main", ".github/workflows/nvoi.yml")
	if !ok {
		t.Fatalf("workflow not written to main; ops=%v", fake.All())
	}
	if !bytes.Contains(content, []byte("cdn.nvoi.to/bin/v0.1.0/")) {
		t.Errorf("workflow missing pinned nvoi version; got:\n%s", content)
	}
	// Secret env wiring for each synced secret must be present, sorted.
	for k := range wantSecrets {
		if !bytes.Contains(content, []byte(k+": ${{ secrets."+k+" }}")) {
			t.Errorf("workflow missing env wiring for %q", k)
		}
	}
	// Env block is sorted — DB_URL appears before HETZNER_TOKEN.
	iDB := bytes.Index(content, []byte("DB_URL:"))
	iHZ := bytes.Index(content, []byte("HETZNER_TOKEN:"))
	if iDB == -1 || iHZ == -1 || iDB > iHZ {
		t.Errorf("workflow secret env is not sorted")
	}
}

func TestCIInit_ProtectedDefault_OpensPR(t *testing.T) {
	cfg := minimalCfg()
	creds := map[string]string{
		"HETZNER_TOKEN":   "hz-token",
		"SSH_PRIVATE_KEY": "fake-pem",
		"GITHUB_TOKEN":    "gh-token",
		"GITHUB_REPO":     "acme/api",
	}
	rt, fake := ciInitFixture(t, cfg, creds)
	fake.SeedRepo("acme", "api", "main")
	fake.SeedRuleset("acme", "api", "main", "pull_request")

	if err := runCIInit(context.Background(), rt, ""); err != nil {
		t.Fatalf("runCIInit: %v", err)
	}
	// Workflow landed on the feature branch, not on main.
	if _, ok := fake.FileContent("acme", "api", "main", ".github/workflows/nvoi.yml"); ok {
		t.Error("workflow should not land on protected main")
	}
	if _, ok := fake.FileContent("acme", "api", "nvoi/ci-init", ".github/workflows/nvoi.yml"); !ok {
		t.Error("workflow should land on nvoi/ci-init feature branch")
	}
	if !fake.Has("github:create-pull:acme/api:nvoi/ci-init->main") {
		t.Errorf("expected PR creation, ops=%v", fake.All())
	}
}

func TestCIInit_MissingSSHKey_Errors(t *testing.T) {
	// Isolate HOME so resolveSSHKey's final on-disk fallback (~/.ssh/id_*)
	// can't find a key on the dev machine running the test.
	t.Setenv("HOME", t.TempDir())

	cfg := minimalCfg()
	// No SSH_PRIVATE_KEY — collector should reject before any HTTP call.
	creds := map[string]string{
		"HETZNER_TOKEN": "hz-token",
		"GITHUB_TOKEN":  "gh-token",
		"GITHUB_REPO":   "acme/api",
	}
	rt, fake := ciInitFixture(t, cfg, creds)
	fake.SeedRepo("acme", "api", "main")

	err := runCIInit(context.Background(), rt, "")
	if err == nil {
		t.Fatal("expected error for missing SSH_PRIVATE_KEY")
	}
	if !strings.Contains(err.Error(), "SSH_PRIVATE_KEY") {
		t.Errorf("expected SSH_PRIVATE_KEY error, got %v", err)
	}
	// Credentials were validated before the collector ran, so /user + repo
	// probes fired — but no secret PUTs or content PUTs should have.
	if f := fake.Count("github:put-secret:"); f != 0 {
		t.Errorf("no secrets should have been synced, got %d", f)
	}
	if f := fake.Count("github:put-contents:"); f != 0 {
		t.Errorf("no content should have been written, got %d", f)
	}
}

func TestCIInit_MissingProvidersCi_Errors(t *testing.T) {
	cfg := minimalCfg()
	// Don't set cfg.Providers.Ci at all — runCIInit must error with a
	// pointer at the missing config key.
	rt := &runtime{
		cfg: cfg,
		out: &testutil.MockOutput{},
		dc: &config.DeployContext{
			Cluster: app.Cluster{Output: &testutil.MockOutput{}},
			Creds:   provider.MapSource{M: map[string]string{}},
		},
	}

	err := runCIInit(context.Background(), rt, "")
	if err == nil {
		t.Fatal("expected error for unset providers.ci")
	}
	if !strings.Contains(err.Error(), "providers.ci is not set") {
		t.Errorf("expected providers.ci guidance, got %v", err)
	}
}

func TestCIInit_RegistryVarRefsPorted(t *testing.T) {
	cfg := minimalCfg()
	cfg.Registry = map[string]config.RegistryDef{
		"ghcr.io": {Username: "$GHCR_USER", Password: "$GHCR_TOKEN"},
	}
	creds := map[string]string{
		"HETZNER_TOKEN":   "hz-token",
		"SSH_PRIVATE_KEY": "fake-pem",
		"GHCR_USER":       "benjam",
		"GHCR_TOKEN":      "ghp_xxx",
		"GITHUB_TOKEN":    "gh-token",
		"GITHUB_REPO":     "acme/api",
	}
	rt, fake := ciInitFixture(t, cfg, creds)
	fake.SeedRepo("acme", "api", "main")

	if err := runCIInit(context.Background(), rt, ""); err != nil {
		t.Fatalf("runCIInit: %v", err)
	}
	// Registry $VAR refs are expanded to their backing env-var names —
	// the runner can resolve them at deploy time.
	if got, _ := fake.SecretValue("acme", "api", "GHCR_USER"); got != "benjam" {
		t.Errorf("GHCR_USER: want benjam got %q", got)
	}
	if got, _ := fake.SecretValue("acme", "api", "GHCR_TOKEN"); got != "ghp_xxx" {
		t.Errorf("GHCR_TOKEN: want ghp_xxx got %q", got)
	}
}

func TestCIInit_ServiceSecretRefsPorted(t *testing.T) {
	cfg := minimalCfg()
	// service/cron refs can be bare (FOO) or KEY=$VAR (ALIAS=$FOO).
	// Both forms must port the backing env var.
	svc := cfg.Services["web"]
	svc.Secrets = []string{"JWT_SECRET", "ALIAS=$DB_PASSWORD"}
	cfg.Services["web"] = svc

	creds := map[string]string{
		"HETZNER_TOKEN":   "hz-token",
		"SSH_PRIVATE_KEY": "fake-pem",
		"JWT_SECRET":      "jwt-value",
		"DB_PASSWORD":     "secret",
		"GITHUB_TOKEN":    "gh-token",
		"GITHUB_REPO":     "acme/api",
	}
	rt, fake := ciInitFixture(t, cfg, creds)
	fake.SeedRepo("acme", "api", "main")

	if err := runCIInit(context.Background(), rt, ""); err != nil {
		t.Fatalf("runCIInit: %v", err)
	}
	// Bare name ported directly.
	if got, _ := fake.SecretValue("acme", "api", "JWT_SECRET"); got != "jwt-value" {
		t.Errorf("JWT_SECRET: want jwt-value got %q", got)
	}
	// ALIAS=$DB_PASSWORD — the env key the service sees is ALIAS, but
	// the runner resolves $DB_PASSWORD, so DB_PASSWORD is what must land
	// in the secret store.
	if got, ok := fake.SecretValue("acme", "api", "ALIAS"); ok {
		t.Errorf("ALIAS (the reference key) should not be synced, got %q", got)
	}
	if got, _ := fake.SecretValue("acme", "api", "DB_PASSWORD"); got != "secret" {
		t.Errorf("DB_PASSWORD: want secret got %q", got)
	}
}
