package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// apiBase is the public GitHub REST endpoint. Tests point the underlying
// HTTPClient at a httptest server URL via SetBaseURL.
const apiBase = "https://api.github.com"

// branchName is the feature branch GitHub writes to when the default
// branch rejects a direct push (rulesets / branch protection).
const branchName = "nvoi/ci-init"

// GitHubCI implements provider.CIProvider against the GitHub REST API.
type GitHubCI struct {
	http  *utils.HTTPClient
	token string
	owner string
	repo  string
}

// New returns a GitHubCI for the given token + repo ("owner/repo"). The
// repo argument may be empty at construction time; callers that don't
// have a remote handy can hand it in later via SetRepo — but today the
// CLI always populates it before calling Resolve.
func New(token, repo string) *GitHubCI {
	c := &GitHubCI{
		http: &utils.HTTPClient{
			BaseURL: apiBase,
			SetAuth: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Accept", "application/vnd.github+json")
				req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
			},
			Label: "github",
		},
		token: token,
	}
	c.owner, c.repo = parseRepo(repo)
	return c
}

// SetBaseURL repoints the underlying HTTP client — test seam only. The
// fake in internal/testutil/providermocks.go calls this after Register
// so real GitHubCI instances hit the httptest server.
func (g *GitHubCI) SetBaseURL(url string) { g.http.BaseURL = url }

// SetRepo overrides the (owner, repo) pair. Used by cmd/cli/ci.go when
// the user's checkout remote is inferred after construction.
func (g *GitHubCI) SetRepo(repo string) {
	g.owner, g.repo = parseRepo(repo)
}

// ── CIProvider ───────────────────────────────────────────────────────────────

// ValidateCredentials probes GET /user to confirm the token is valid and
// has at least the basic read scope. A bad token fails loud here instead
// of halfway through the secret sync.
func (g *GitHubCI) ValidateCredentials(ctx context.Context) error {
	if strings.TrimSpace(g.token) == "" {
		return fmt.Errorf("github: GITHUB_TOKEN is empty")
	}
	if g.owner == "" || g.repo == "" {
		return fmt.Errorf("github: repo is empty — set GITHUB_REPO (owner/repo) or run from a git checkout with a GitHub remote")
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := g.http.Do(ctx, "GET", "/user", nil, &user); err != nil {
		return fmt.Errorf("github: validate token: %w", err)
	}
	// Confirm the token can see the repo too — cheaper than discovering
	// a 404 mid-workflow-commit.
	if err := g.http.Do(ctx, "GET", repoPath(g.owner, g.repo), nil, nil); err != nil {
		return fmt.Errorf("github: access %s/%s: %w", g.owner, g.repo, err)
	}
	return nil
}

func (g *GitHubCI) Target() provider.CITarget {
	return provider.CITarget{
		Kind:  "github",
		Owner: g.owner,
		Repo:  g.repo,
		URL:   fmt.Sprintf("https://github.com/%s/%s", g.owner, g.repo),
	}
}

// SyncSecrets seals each value with libsodium's anonymous-sealed-box
// against the repo's public key and uploads it. GitHub stores the
// sealed ciphertext; the runner decrypts with the matching private key
// (never exposed to us). Empty map = no-op.
func (g *GitHubCI) SyncSecrets(ctx context.Context, secrets map[string]string) error {
	if len(secrets) == 0 {
		return nil
	}
	pk, err := g.getPublicKey(ctx)
	if err != nil {
		return err
	}
	// Order keys so repeated runs produce deterministic API call sequences
	// (testable, auditable). OpLog in tests asserts the sequence.
	names := sortedKeys(secrets)
	for _, name := range names {
		sealed, err := sealSecret(pk.Key, secrets[name])
		if err != nil {
			return fmt.Errorf("github: seal %s: %w", name, err)
		}
		body := putSecretRequest{
			EncryptedValue: sealed,
			KeyID:          pk.KeyID,
		}
		path := fmt.Sprintf("%s/actions/secrets/%s", repoPath(g.owner, g.repo), name)
		if err := g.http.Do(ctx, "PUT", path, body, nil); err != nil {
			return fmt.Errorf("github: put secret %s: %w", name, err)
		}
	}
	return nil
}

// CommitFiles writes the given files via the Contents API. On a
// protected default branch the direct PUT returns 409/422; we fall back
// to creating `nvoi/ci-init` and opening a PR.
func (g *GitHubCI) CommitFiles(ctx context.Context, files []provider.CIFile, message string) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	defaultBranch, err := g.getDefaultBranch(ctx)
	if err != nil {
		return "", err
	}

	// Rulesets / branch protection are two separate GitHub features. We
	// check rulesets (the newer, broader mechanism) upfront so we never
	// attempt a direct push the server will reject. Branch protection
	// typically also shows up as a ruleset in modern repos; on older
	// repos we still rely on the 409/422 fallback inside commitToBranch.
	protected, err := g.defaultBranchProtected(ctx, defaultBranch)
	if err != nil {
		return "", err
	}

	if !protected {
		if err := g.commitToBranch(ctx, files, defaultBranch, message); err != nil {
			// If the server still rejected (unmapped rules, branch
			// protection without rulesets), fall through to the PR path.
			if !isProtectedBranchError(err) {
				return "", err
			}
			protected = true
		} else {
			return fmt.Sprintf("https://github.com/%s/%s/commits/%s", g.owner, g.repo, defaultBranch), nil
		}
	}

	// Protected-branch path: create the feature branch, commit there, open PR.
	if err := g.ensureBranch(ctx, branchName, defaultBranch); err != nil {
		return "", err
	}
	if err := g.commitToBranch(ctx, files, branchName, message); err != nil {
		return "", err
	}
	prURL, err := g.openPR(ctx, branchName, defaultBranch, message)
	if err != nil {
		return "", err
	}
	return prURL, nil
}

// RenderWorkflow returns the GitHub Actions workflow for this plan. Pure
// function — no network, no git. Lives in workflow.go.
func (g *GitHubCI) RenderWorkflow(plan provider.CIWorkflowPlan) (string, []byte, error) {
	return renderWorkflow(plan)
}

// ListResources surfaces the synced secrets + any open nvoi/ci-init PR.
// Keeps `nvoi resources` aware of SaaS-mode onboarding state.
func (g *GitHubCI) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	groups := []provider.ResourceGroup{}
	// Secrets listing — names only, never values (GitHub's API doesn't
	// expose values anyway).
	var secretList struct {
		Secrets []struct {
			Name string `json:"name"`
		} `json:"secrets"`
	}
	path := fmt.Sprintf("%s/actions/secrets?per_page=100", repoPath(g.owner, g.repo))
	if err := g.http.Do(ctx, "GET", path, nil, &secretList); err == nil {
		rows := make([][]string, len(secretList.Secrets))
		for i, s := range secretList.Secrets {
			rows[i] = []string{s.Name}
		}
		groups = append(groups, provider.ResourceGroup{
			Name:    "github-actions-secrets",
			Columns: []string{"name"},
			Rows:    rows,
		})
	}
	return groups, nil
}

func (g *GitHubCI) Close() error { return nil }

// ── GitHub API helpers ───────────────────────────────────────────────────────

// publicKey is the response from GET /repos/{o}/{r}/actions/secrets/public-key.
// Key is base64-encoded raw bytes (32 bytes after decode for curve25519).
// KeyID is the opaque identifier paired with each sealed secret.
type publicKey struct {
	Key   string `json:"key"`
	KeyID string `json:"key_id"`
}

// putSecretRequest is the body of PUT /repos/{o}/{r}/actions/secrets/{name}.
type putSecretRequest struct {
	EncryptedValue string `json:"encrypted_value"`
	KeyID          string `json:"key_id"`
}

func (g *GitHubCI) getPublicKey(ctx context.Context) (publicKey, error) {
	var pk publicKey
	path := fmt.Sprintf("%s/actions/secrets/public-key", repoPath(g.owner, g.repo))
	if err := g.http.Do(ctx, "GET", path, nil, &pk); err != nil {
		return publicKey{}, fmt.Errorf("github: fetch public key: %w", err)
	}
	return pk, nil
}

func (g *GitHubCI) getDefaultBranch(ctx context.Context) (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.http.Do(ctx, "GET", repoPath(g.owner, g.repo), nil, &r); err != nil {
		return "", fmt.Errorf("github: fetch repo metadata: %w", err)
	}
	if r.DefaultBranch == "" {
		return "main", nil
	}
	return r.DefaultBranch, nil
}

// commitToBranch PUTs each file via the Contents API. Each file gets its
// own commit — one PUT per file is the API shape. For our small workflow
// payload this is acceptable; if we ever emit many files we'd switch to
// the lower-level git-trees / git-commits endpoints.
func (g *GitHubCI) commitToBranch(ctx context.Context, files []provider.CIFile, branch, message string) error {
	for _, f := range files {
		// Look up the existing file SHA (required for updates; absent for
		// new files). We ignore a 404 — that means the file is new.
		var meta struct {
			SHA string `json:"sha"`
		}
		contentPath := fmt.Sprintf("%s/contents/%s?ref=%s", repoPath(g.owner, g.repo), f.Path, branch)
		if err := g.http.Do(ctx, "GET", contentPath, nil, &meta); err != nil {
			if !provider.IsNotFound(err) {
				return fmt.Errorf("github: lookup %s: %w", f.Path, err)
			}
		}
		body := map[string]any{
			"message": message,
			"content": base64.StdEncoding.EncodeToString(f.Content),
			"branch":  branch,
		}
		if meta.SHA != "" {
			body["sha"] = meta.SHA
		}
		putPath := fmt.Sprintf("%s/contents/%s", repoPath(g.owner, g.repo), f.Path)
		if err := g.http.Do(ctx, "PUT", putPath, body, nil); err != nil {
			return fmt.Errorf("github: commit %s to %s: %w", f.Path, branch, err)
		}
	}
	return nil
}

// ensureBranch creates `branch` pointing at the HEAD of `base` if it
// doesn't exist. Idempotent — re-running `nvoi ci init` updates the
// existing branch via commitToBranch without recreating the ref.
func (g *GitHubCI) ensureBranch(ctx context.Context, branch, base string) error {
	// Does it already exist?
	refPath := fmt.Sprintf("%s/git/ref/heads/%s", repoPath(g.owner, g.repo), branch)
	if err := g.http.Do(ctx, "GET", refPath, nil, nil); err == nil {
		return nil
	}
	// Get base SHA.
	var baseRef struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	basePath := fmt.Sprintf("%s/git/ref/heads/%s", repoPath(g.owner, g.repo), base)
	if err := g.http.Do(ctx, "GET", basePath, nil, &baseRef); err != nil {
		return fmt.Errorf("github: fetch base ref %s: %w", base, err)
	}
	// Create the branch ref.
	createPath := fmt.Sprintf("%s/git/refs", repoPath(g.owner, g.repo))
	body := map[string]string{
		"ref": "refs/heads/" + branch,
		"sha": baseRef.Object.SHA,
	}
	if err := g.http.Do(ctx, "POST", createPath, body, nil); err != nil {
		return fmt.Errorf("github: create branch %s: %w", branch, err)
	}
	return nil
}

// openPR creates a PR head→base. If a PR with the same head already
// exists, return its URL — nvoi ci init is idempotent.
func (g *GitHubCI) openPR(ctx context.Context, head, base, title string) (string, error) {
	// Idempotency: look for an existing open PR first.
	listPath := fmt.Sprintf("%s/pulls?state=open&head=%s:%s&base=%s",
		repoPath(g.owner, g.repo), g.owner, head, base)
	var existing []struct {
		URL string `json:"html_url"`
	}
	if err := g.http.Do(ctx, "GET", listPath, nil, &existing); err == nil && len(existing) > 0 {
		return existing[0].URL, nil
	}
	body := map[string]any{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  "Automated PR from `nvoi ci init`. Review and merge to enable `git push` deploys.",
	}
	var created struct {
		URL string `json:"html_url"`
	}
	createPath := fmt.Sprintf("%s/pulls", repoPath(g.owner, g.repo))
	if err := g.http.Do(ctx, "POST", createPath, body, &created); err != nil {
		return "", fmt.Errorf("github: open PR: %w", err)
	}
	return created.URL, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// parseRepo accepts every remote-URL form `git remote get-url origin`
// returns and reduces it to (owner, repo). Empty string → ("", "").
func parseRepo(repo string) (owner, name string) {
	s := strings.TrimSpace(repo)
	if s == "" {
		return "", ""
	}
	// Strip trailing .git and trailing slash.
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	// Accept the plain `owner/repo` form first — by far the commonest
	// user-facing shape, and we always canonicalize to this form after
	// parsing a URL anyway.
	if !strings.Contains(s, ":") && !strings.Contains(s, "/") {
		return "", ""
	}
	if !strings.Contains(s, "://") && !strings.Contains(s, "@") && strings.Count(s, "/") == 1 {
		parts := strings.SplitN(s, "/", 2)
		return parts[0], parts[1]
	}

	// ssh://git@github.com/owner/repo → host-less path
	if strings.HasPrefix(s, "ssh://") {
		s = strings.TrimPrefix(s, "ssh://")
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:]
		}
		if slash := strings.Index(s, "/"); slash >= 0 {
			s = s[slash+1:]
		}
	} else if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		// https://github.com/owner/repo
		if i := strings.Index(s, "://"); i >= 0 {
			s = s[i+3:]
		}
		if slash := strings.Index(s, "/"); slash >= 0 {
			s = s[slash+1:]
		}
	} else if at := strings.Index(s, "@"); at >= 0 {
		// git@github.com:owner/repo
		s = s[at+1:]
		if colon := strings.Index(s, ":"); colon >= 0 {
			s = s[colon+1:]
		}
	}

	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func repoPath(owner, repo string) string {
	return fmt.Sprintf("/repos/%s/%s", owner, repo)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Stable order — strings.sort style without pulling sort into an
	// already-tiny helper file.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
