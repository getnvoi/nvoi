package github_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/github"
)

// newResolved returns a resolved GitHubCI pointed at the fake. Mirrors what
// cmd/cli/ci.go does in production — schema lookup + ResolveCI + SetBaseURL —
// so the test exercises the public entry path, not a direct constructor.
func newResolved(t *testing.T, f *testutil.GitHubFake, repo string) provider.CIProvider {
	t.Helper()
	// Register under a unique-per-test name so parallel subtests don't clash.
	name := "github-" + t.Name()
	f.Register(name)
	p, err := provider.ResolveCI(name, map[string]string{
		"token": "fake-token",
		"repo":  repo,
	})
	if err != nil {
		t.Fatalf("ResolveCI: %v", err)
	}
	return p
}

func TestValidateCredentials_OK(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	f.SeedRepo("acme", "api", "main")
	p := newResolved(t, f, "acme/api")

	if err := p.ValidateCredentials(context.Background()); err != nil {
		t.Fatalf("ValidateCredentials: %v", err)
	}
	if !f.Has("github:get-user") {
		t.Errorf("expected /user probe, got %v", f.All())
	}
	if !f.Has("github:get-repo:acme/api") {
		t.Errorf("expected repo probe, got %v", f.All())
	}
}

func TestValidateCredentials_MissingRepo(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	p := newResolved(t, f, "")

	err := p.ValidateCredentials(context.Background())
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
	if !strings.Contains(err.Error(), "repo is empty") {
		t.Errorf("expected repo-empty error, got %v", err)
	}
}

func TestSyncSecrets_RoundTrip(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	f.SeedRepo("acme", "api", "main")
	p := newResolved(t, f, "acme/api")

	secrets := map[string]string{
		"FOO": "bar-value",
		"BAZ": "hunter2",
	}
	if err := p.SyncSecrets(context.Background(), secrets); err != nil {
		t.Fatalf("SyncSecrets: %v", err)
	}
	// Assert sealed-box correctness end-to-end: fake decrypted what
	// GitHubCI sealed, so plaintext round-trips.
	for k, v := range secrets {
		got, ok := f.SecretValue("acme", "api", k)
		if !ok {
			t.Fatalf("secret %q not stored", k)
		}
		if got != v {
			t.Errorf("secret %q round-trip: want %q got %q", k, v, got)
		}
	}
	// Deterministic order: keys sorted lexicographically.
	if idxBaz, idxFoo := f.IndexOf("github:put-secret:acme/api:BAZ"), f.IndexOf("github:put-secret:acme/api:FOO"); idxBaz > idxFoo {
		t.Errorf("expected BAZ before FOO, got ops %v", f.All())
	}
	// Public key fetched exactly once across all secrets (no N+1).
	if f.Count("github:get-public-key") != 1 {
		t.Errorf("expected single public-key fetch, got %d", f.Count("github:get-public-key"))
	}
}

func TestSyncSecrets_EmptyIsNoop(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	f.SeedRepo("acme", "api", "main")
	p := newResolved(t, f, "acme/api")

	if err := p.SyncSecrets(context.Background(), nil); err != nil {
		t.Fatalf("SyncSecrets empty: %v", err)
	}
	if f.Count("github:get-public-key") != 0 {
		t.Error("empty SyncSecrets should not fetch public key")
	}
}

func TestCommitFiles_DirectPush(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	f.SeedRepo("acme", "api", "main")
	p := newResolved(t, f, "acme/api")

	files := []provider.CIFile{{Path: ".github/workflows/nvoi.yml", Content: []byte("hello")}}
	url, err := p.CommitFiles(context.Background(), files, "chore: nvoi ci init")
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if !strings.Contains(url, "/commits/main") {
		t.Errorf("expected commit URL pointing at main, got %q", url)
	}
	// File landed on main, not on a feature branch, and no PR opened.
	if got, ok := f.FileContent("acme", "api", "main", ".github/workflows/nvoi.yml"); !ok || !bytes.Equal(got, []byte("hello")) {
		t.Errorf("file not written to main; got %q ok=%v", got, ok)
	}
	if f.Count("github:create-ref:") != 0 {
		t.Errorf("unprotected path should not create a feature branch, ops %v", f.All())
	}
	if f.Count("github:create-pull:") != 0 {
		t.Errorf("unprotected path should not open a PR, ops %v", f.All())
	}
}

func TestCommitFiles_ProtectedByRuleset(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	f.SeedRepo("acme", "api", "main")
	f.SeedRuleset("acme", "api", "main", "pull_request")
	p := newResolved(t, f, "acme/api")

	files := []provider.CIFile{{Path: ".github/workflows/nvoi.yml", Content: []byte("hello")}}
	url, err := p.CommitFiles(context.Background(), files, "chore: nvoi ci init")
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if !strings.Contains(url, "/pull/1") {
		t.Errorf("expected PR URL, got %q", url)
	}
	// File landed on the feature branch, not main.
	if _, ok := f.FileContent("acme", "api", "main", ".github/workflows/nvoi.yml"); ok {
		t.Error("protected-branch path should not write to main")
	}
	if got, ok := f.FileContent("acme", "api", "nvoi/ci-init", ".github/workflows/nvoi.yml"); !ok || !bytes.Equal(got, []byte("hello")) {
		t.Errorf("file not written to nvoi/ci-init; got %q ok=%v", got, ok)
	}
	if !f.Has("github:create-ref:acme/api:nvoi/ci-init") {
		t.Errorf("expected feature branch creation, ops %v", f.All())
	}
	if !f.Has("github:create-pull:acme/api:nvoi/ci-init->main") {
		t.Errorf("expected PR acme/api:nvoi/ci-init->main, ops %v", f.All())
	}
}

func TestCommitFiles_ProtectedByClassicProtection(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	f.SeedRepo("acme", "api", "main")
	f.SeedProtection("acme", "api", "main")
	p := newResolved(t, f, "acme/api")

	files := []provider.CIFile{{Path: ".github/workflows/nvoi.yml", Content: []byte("hello")}}
	url, err := p.CommitFiles(context.Background(), files, "chore: nvoi ci init")
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if !strings.Contains(url, "/pull/1") {
		t.Errorf("expected PR URL, got %q", url)
	}
}

func TestCommitFiles_IdempotentPROnRerun(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	f.SeedRepo("acme", "api", "main")
	f.SeedRuleset("acme", "api", "main", "pull_request")
	p := newResolved(t, f, "acme/api")

	files := []provider.CIFile{{Path: ".github/workflows/nvoi.yml", Content: []byte("v1")}}
	url1, err := p.CommitFiles(context.Background(), files, "m1")
	if err != nil {
		t.Fatalf("first CommitFiles: %v", err)
	}
	// Second run with different content — branch exists, PR exists.
	files[0].Content = []byte("v2")
	url2, err := p.CommitFiles(context.Background(), files, "m2")
	if err != nil {
		t.Fatalf("second CommitFiles: %v", err)
	}
	if url1 != url2 {
		t.Errorf("expected same PR URL, got %q vs %q", url1, url2)
	}
	// Content was updated on the feature branch.
	if got, _ := f.FileContent("acme", "api", "nvoi/ci-init", ".github/workflows/nvoi.yml"); !bytes.Equal(got, []byte("v2")) {
		t.Errorf("expected content v2, got %q", got)
	}
	// Exactly one create-ref, exactly one create-pull across both runs.
	if f.Count("github:create-ref:") != 1 {
		t.Errorf("expected 1 create-ref, got %d: %v", f.Count("github:create-ref:"), f.All())
	}
	if f.Count("github:create-pull:") != 1 {
		t.Errorf("expected 1 create-pull, got %d: %v", f.Count("github:create-pull:"), f.All())
	}
}

func TestRenderWorkflow_Deterministic(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	f.SeedRepo("acme", "api", "main")
	p := newResolved(t, f, "acme/api")

	plan := provider.CIWorkflowPlan{
		NvoiVersion: "v0.1.0",
		SecretEnv:   []string{"HETZNER_TOKEN", "CF_API_KEY"},
	}
	path1, content1, err := p.RenderWorkflow(plan)
	if err != nil {
		t.Fatalf("RenderWorkflow: %v", err)
	}
	path2, content2, err := p.RenderWorkflow(plan)
	if err != nil {
		t.Fatalf("RenderWorkflow: %v", err)
	}
	if path1 != ".github/workflows/nvoi.yml" {
		t.Errorf("path = %q, want .github/workflows/nvoi.yml", path1)
	}
	if path1 != path2 || !bytes.Equal(content1, content2) {
		t.Error("RenderWorkflow is not deterministic")
	}
	s := string(content1)
	for _, v := range plan.SecretEnv {
		if !strings.Contains(s, v+": ${{ secrets."+v+" }}") {
			t.Errorf("workflow missing secret env wiring for %q", v)
		}
	}
	if !strings.Contains(s, "cdn.nvoi.to/bin/v0.1.0/") {
		t.Error("workflow should pin nvoi version in download URL")
	}
}

func TestRenderWorkflow_RejectsEmptySecretName(t *testing.T) {
	f := testutil.NewGitHubFake(t)
	f.SeedRepo("acme", "api", "main")
	p := newResolved(t, f, "acme/api")

	plan := provider.CIWorkflowPlan{SecretEnv: []string{"GOOD", ""}}
	if _, _, err := p.RenderWorkflow(plan); err == nil {
		t.Fatal("expected error for empty secret name")
	}
}

func TestParseRepo(t *testing.T) {
	// parseRepo is unexported, but SetRepo drives it and Target exposes
	// the parsed pair — exercise that observable surface instead of
	// reaching into the package.
	cases := []struct {
		in, owner, repo string
	}{
		{"acme/api", "acme", "api"},
		{"https://github.com/acme/api", "acme", "api"},
		{"https://github.com/acme/api.git", "acme", "api"},
		{"git@github.com:acme/api.git", "acme", "api"},
		{"ssh://git@github.com/acme/api.git", "acme", "api"},
		{"", "", ""},
		{"not-a-repo", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			c := github.New("tok", tc.in)
			got := c.Target()
			if got.Owner != tc.owner || got.Repo != tc.repo {
				t.Errorf("parseRepo(%q) = (%q, %q); want (%q, %q)",
					tc.in, got.Owner, got.Repo, tc.owner, tc.repo)
			}
		})
	}
}
