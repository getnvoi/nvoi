package testutil

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/nacl/box"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/github"
)

// ── GitHubFake ────────────────────────────────────────────────────────────────

// GitHubFake is a stateful, in-memory GitHub REST API covering every endpoint
// the CIProvider for `github` touches: /user, /repos/{o}/{r}, Actions secrets
// (public-key + PUT + list), Contents, git refs, pulls, rulesets, and classic
// branch protection.
//
// The fake generates a real curve25519 keypair at construction — the public
// half is exposed via /actions/secrets/public-key, the private half stays
// internal. Sealed secrets that tests PUT are decrypted on the spot, so the
// fake holds plaintext values that tests assert against (sealed-box
// correctness is verified end-to-end without reimplementing sealing).
//
// State model is repo-scoped via SeedRepo — the fake refuses any request to
// an unseeded (owner, repo) pair. SeedRuleset marks a branch as protected;
// SeedProtection simulates a classic branch-protection object. A protected
// default branch makes the Contents PUT return 422 with the standard
// "protected branch" body, triggering GitHubCI's PR fallback path.
type GitHubFake struct {
	*httptest.Server
	*OpLog

	mu         sync.Mutex
	userLogin  string
	publicKey  [32]byte
	privateKey [32]byte
	keyID      string

	repos map[string]*ghRepo // "owner/repo" → repo
}

type ghRepo struct {
	Owner         string
	Name          string
	DefaultBranch string

	// Secrets: name → plaintext (decrypted on PUT).
	Secrets map[string]string

	// Rulesets: branch → non-empty rule list ⇒ protected.
	Rulesets map[string][]string

	// Classic protection: branch → protected bool.
	Protection map[string]bool

	// Contents: (branch, path) → content bytes. Includes commit SHAs
	// synthesized per write so the Contents API's SHA-update flow works.
	Contents map[string]*ghFile

	// Branches: name → head SHA.
	Branches map[string]string

	// PRs opened via POST /pulls. head → URL.
	PRs map[string]string

	// Monotonic SHA source so every PUT produces a fresh SHA.
	shaSeq int
}

type ghFile struct {
	SHA     string
	Content []byte
}

// NewGitHubFake returns a running GitHub API fake. Pass nil for Cleanup to
// manage lifetime manually.
func NewGitHubFake(t Cleanup) *GitHubFake {
	f := &GitHubFake{
		OpLog:     NewOpLog(),
		userLogin: "testuser",
		keyID:     "key-1",
		repos:     map[string]*ghRepo{},
	}
	// Generate a real sealed-box keypair so the fake can decrypt secrets
	// that the provider seals. Fixed-length [32]byte — no fallback.
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		panic(fmt.Sprintf("GitHubFake: generate keypair: %v", err))
	}
	f.publicKey = *pub
	f.privateKey = *priv
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	if t != nil {
		t.Cleanup(func() { f.Server.Close() })
	}
	return f
}

// Register binds this fake as a CI provider named `name`. The real
// github.GitHubCI is constructed and its BaseURL is repointed at the fake.
func (f *GitHubFake) Register(name string) {
	schema := provider.CredentialSchema{
		Name: name,
		Fields: []provider.CredentialField{
			{Key: "token", Required: true, EnvVar: "GITHUB_TOKEN", Flag: "github-token"},
			{Key: "repo", Required: false, EnvVar: "GITHUB_REPO", Flag: "github-repo"},
		},
	}
	provider.RegisterCI(name, schema, func(creds map[string]string) provider.CIProvider {
		c := github.New(creds["token"], creds["repo"])
		c.SetBaseURL(f.URL)
		return c
	})
}

// ── GitHubFake: seeders ──

// SeedRepo inserts a repo the fake knows about. Required before any other
// API call against (owner, repo) succeeds — reflects how GitHub itself
// behaves (unknown repo ⇒ 404).
func (f *GitHubFake) SeedRepo(owner, repo, defaultBranch string) *ghRepo {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := owner + "/" + repo
	r := &ghRepo{
		Owner:         owner,
		Name:          repo,
		DefaultBranch: defaultBranch,
		Secrets:       map[string]string{},
		Rulesets:      map[string][]string{},
		Protection:    map[string]bool{},
		Contents:      map[string]*ghFile{},
		Branches:      map[string]string{defaultBranch: "sha-base-0"},
		PRs:           map[string]string{},
	}
	f.repos[key] = r
	return r
}

// SeedRuleset marks `branch` as covered by a repository ruleset. The
// ruleset list contents don't matter for our checks — only "non-empty"
// does — so rules is a free-form label list.
func (f *GitHubFake) SeedRuleset(owner, repo, branch string, rules ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.repos[owner+"/"+repo]
	if !ok {
		panic(fmt.Sprintf("SeedRuleset: repo %s/%s not seeded", owner, repo))
	}
	r.Rulesets[branch] = append([]string{}, rules...)
}

// SeedProtection marks `branch` as covered by classic branch protection.
// Independent of rulesets — both gates produce protected behavior.
func (f *GitHubFake) SeedProtection(owner, repo, branch string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.repos[owner+"/"+repo]
	if !ok {
		panic(fmt.Sprintf("SeedProtection: repo %s/%s not seeded", owner, repo))
	}
	r.Protection[branch] = true
}

// SecretValue returns the decrypted plaintext of a secret previously
// synced via SyncSecrets. Tests use this to assert the provider sealed
// the right value against the right key.
func (f *GitHubFake) SecretValue(owner, repo, name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.repos[owner+"/"+repo]
	if !ok {
		return "", false
	}
	v, ok := r.Secrets[name]
	return v, ok
}

// FileContent returns the bytes of a file previously committed via the
// Contents API, from the named branch.
func (f *GitHubFake) FileContent(owner, repo, branch, path string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.repos[owner+"/"+repo]
	if !ok {
		return nil, false
	}
	g, ok := r.Contents[branch+":"+path]
	if !ok {
		return nil, false
	}
	return append([]byte{}, g.Content...), true
}

// ── GitHubFake: HTTP handler ──

// ghRepoPathRE extracts owner and repo from /repos/{owner}/{repo}/... paths.
var ghRepoPathRE = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)(/.*)?$`)

func (f *GitHubFake) serve(w http.ResponseWriter, r *http.Request) {
	// /user — token probe.
	if r.URL.Path == "/user" && r.Method == "GET" {
		if err := f.Record("github:get-user"); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"login": f.userLogin})
		return
	}

	m := ghRepoPathRE.FindStringSubmatch(r.URL.Path)
	if m == nil {
		writeGHError(w, 404, "not found: "+r.URL.Path)
		return
	}
	owner, repo, rest := m[1], m[2], m[3]

	f.mu.Lock()
	repoState, ok := f.repos[owner+"/"+repo]
	f.mu.Unlock()
	if !ok {
		writeGHError(w, 404, "unknown repo "+owner+"/"+repo)
		return
	}

	// Dispatch on sub-path.
	switch {
	case rest == "" || rest == "/":
		f.handleRepoRoot(w, r, repoState)
	case rest == "/actions/secrets/public-key":
		f.handlePublicKey(w, r)
	case rest == "/actions/secrets" || strings.HasPrefix(rest, "/actions/secrets?"):
		f.handleListSecrets(w, r, repoState)
	case strings.HasPrefix(rest, "/actions/secrets/"):
		name := strings.TrimPrefix(rest, "/actions/secrets/")
		f.handlePutSecret(w, r, repoState, name)
	case strings.HasPrefix(rest, "/rules/branches/"):
		branch := strings.TrimPrefix(rest, "/rules/branches/")
		f.handleRulesets(w, r, repoState, branch)
	case strings.HasPrefix(rest, "/branches/") && strings.HasSuffix(rest, "/protection"):
		branch := strings.TrimSuffix(strings.TrimPrefix(rest, "/branches/"), "/protection")
		f.handleProtection(w, r, repoState, branch)
	case strings.HasPrefix(rest, "/contents/"):
		path := strings.TrimPrefix(rest, "/contents/")
		f.handleContents(w, r, repoState, path)
	case strings.HasPrefix(rest, "/git/ref/heads/"):
		branch := strings.TrimPrefix(rest, "/git/ref/heads/")
		f.handleGetRef(w, r, repoState, branch)
	case rest == "/git/refs":
		f.handleCreateRef(w, r, repoState)
	case rest == "/pulls" || strings.HasPrefix(rest, "/pulls?"):
		f.handlePulls(w, r, repoState)
	default:
		writeGHError(w, 404, "unmapped path "+rest)
	}
}

func (f *GitHubFake) handleRepoRoot(w http.ResponseWriter, r *http.Request, repo *ghRepo) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:get-repo:%s/%s", repo.Owner, repo.Name)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"full_name":      repo.Owner + "/" + repo.Name,
		"default_branch": repo.DefaultBranch,
	})
}

func (f *GitHubFake) handlePublicKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record("github:get-public-key"); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"key":    base64.StdEncoding.EncodeToString(f.publicKey[:]),
		"key_id": f.keyID,
	})
}

func (f *GitHubFake) handleListSecrets(w http.ResponseWriter, r *http.Request, repo *ghRepo) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:list-secrets:%s/%s", repo.Owner, repo.Name)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	secrets := make([]map[string]string, 0, len(repo.Secrets))
	for name := range repo.Secrets {
		secrets = append(secrets, map[string]string{"name": name})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"total_count": len(secrets),
		"secrets":     secrets,
	})
}

func (f *GitHubFake) handlePutSecret(w http.ResponseWriter, r *http.Request, repo *ghRepo, name string) {
	if r.Method != "PUT" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:put-secret:%s/%s:%s", repo.Owner, repo.Name, name)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	var body struct {
		EncryptedValue string `json:"encrypted_value"`
		KeyID          string `json:"key_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeGHError(w, 400, "decode body: "+err.Error())
		return
	}
	if body.KeyID != f.keyID {
		writeGHError(w, 422, "wrong key_id")
		return
	}
	ciphertext, err := base64.StdEncoding.DecodeString(body.EncryptedValue)
	if err != nil {
		writeGHError(w, 422, "bad base64")
		return
	}
	plaintext, ok := box.OpenAnonymous(nil, ciphertext, &f.publicKey, &f.privateKey)
	if !ok {
		writeGHError(w, 422, "sealed-box decrypt failed")
		return
	}
	f.mu.Lock()
	repo.Secrets[name] = string(plaintext)
	f.mu.Unlock()
	w.WriteHeader(201)
}

func (f *GitHubFake) handleRulesets(w http.ResponseWriter, r *http.Request, repo *ghRepo, branch string) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:get-rulesets:%s/%s:%s", repo.Owner, repo.Name, branch)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	rules := repo.Rulesets[branch]
	f.mu.Unlock()
	out := make([]map[string]string, len(rules))
	for i, r := range rules {
		out[i] = map[string]string{"type": r}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *GitHubFake) handleProtection(w http.ResponseWriter, r *http.Request, repo *ghRepo, branch string) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:get-protection:%s/%s:%s", repo.Owner, repo.Name, branch)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	protected := repo.Protection[branch]
	f.mu.Unlock()
	if !protected {
		writeGHError(w, 404, "not protected")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"url": "protection-url"})
}

func (f *GitHubFake) handleContents(w http.ResponseWriter, r *http.Request, repo *ghRepo, path string) {
	branch := r.URL.Query().Get("ref")
	switch r.Method {
	case "GET":
		if branch == "" {
			branch = repo.DefaultBranch
		}
		if err := f.Record(fmt.Sprintf("github:get-contents:%s/%s:%s:%s", repo.Owner, repo.Name, branch, path)); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		f.mu.Lock()
		g, ok := repo.Contents[branch+":"+path]
		f.mu.Unlock()
		if !ok {
			writeGHError(w, 404, "no file")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": g.SHA, "path": path})
	case "PUT":
		var body struct {
			Message string `json:"message"`
			Content string `json:"content"`
			Branch  string `json:"branch"`
			SHA     string `json:"sha"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeGHError(w, 400, "decode body: "+err.Error())
			return
		}
		if body.Branch == "" {
			body.Branch = repo.DefaultBranch
		}
		if err := f.Record(fmt.Sprintf("github:put-contents:%s/%s:%s:%s", repo.Owner, repo.Name, body.Branch, path)); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		// Reject direct push on protected default branch.
		f.mu.Lock()
		isDefault := body.Branch == repo.DefaultBranch
		isRuleset := len(repo.Rulesets[body.Branch]) > 0
		isClassic := repo.Protection[body.Branch]
		f.mu.Unlock()
		if isDefault && (isRuleset || isClassic) {
			writeGHError(w, 422, "repository rule violations: protected branch disallows direct push")
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(body.Content)
		if err != nil {
			writeGHError(w, 422, "bad base64 content")
			return
		}
		f.mu.Lock()
		repo.shaSeq++
		sha := fmt.Sprintf("sha-%d", repo.shaSeq)
		repo.Contents[body.Branch+":"+path] = &ghFile{SHA: sha, Content: decoded}
		repo.Branches[body.Branch] = sha
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"sha": sha, "path": path}})
	default:
		writeGHError(w, 405, "method not allowed")
	}
}

func (f *GitHubFake) handleGetRef(w http.ResponseWriter, r *http.Request, repo *ghRepo, branch string) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:get-ref:%s/%s:%s", repo.Owner, repo.Name, branch)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	sha, ok := repo.Branches[branch]
	f.mu.Unlock()
	if !ok {
		writeGHError(w, 404, "no ref")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ref":    "refs/heads/" + branch,
		"object": map[string]string{"sha": sha},
	})
}

func (f *GitHubFake) handleCreateRef(w http.ResponseWriter, r *http.Request, repo *ghRepo) {
	if r.Method != "POST" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeGHError(w, 400, "decode body: "+err.Error())
		return
	}
	branch := strings.TrimPrefix(body.Ref, "refs/heads/")
	if err := f.Record(fmt.Sprintf("github:create-ref:%s/%s:%s", repo.Owner, repo.Name, branch)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	if _, exists := repo.Branches[branch]; exists {
		f.mu.Unlock()
		writeGHError(w, 422, "ref already exists")
		return
	}
	repo.Branches[branch] = body.SHA
	f.mu.Unlock()
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(map[string]any{"ref": body.Ref, "object": map[string]string{"sha": body.SHA}})
}

func (f *GitHubFake) handlePulls(w http.ResponseWriter, r *http.Request, repo *ghRepo) {
	switch r.Method {
	case "GET":
		headQ := r.URL.Query().Get("head") // "owner:branch"
		if err := f.Record(fmt.Sprintf("github:list-pulls:%s/%s:%s", repo.Owner, repo.Name, headQ)); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		// Strip "owner:" prefix to get the branch name for lookup.
		branch := headQ
		if idx := strings.Index(headQ, ":"); idx >= 0 {
			branch = headQ[idx+1:]
		}
		f.mu.Lock()
		url, ok := repo.PRs[branch]
		f.mu.Unlock()
		if !ok {
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{{"html_url": url}})
	case "POST":
		var body struct {
			Title string `json:"title"`
			Head  string `json:"head"`
			Base  string `json:"base"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeGHError(w, 400, "decode body: "+err.Error())
			return
		}
		if err := f.Record(fmt.Sprintf("github:create-pull:%s/%s:%s->%s", repo.Owner, repo.Name, body.Head, body.Base)); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		f.mu.Lock()
		if _, exists := repo.PRs[body.Head]; exists {
			f.mu.Unlock()
			writeGHError(w, 422, "PR already exists")
			return
		}
		url := fmt.Sprintf("https://github.com/%s/%s/pull/1", repo.Owner, repo.Name)
		repo.PRs[body.Head] = url
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"html_url": url, "number": 1})
	default:
		writeGHError(w, 405, "method not allowed")
	}
}
