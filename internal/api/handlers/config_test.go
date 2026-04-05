package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const validYAML = `
servers:
  master:
    type: cx23
    region: fsn1
volumes:
  pgdata:
    size: 30
    server: master
build:
  web:
    source: benbonnet/dummy-rails
storage:
  assets:
    cors: true
services:
  db:
    image: postgres:17
    volumes:
      - pgdata:/var/lib/postgresql/data
    secrets:
      - POSTGRES_PASSWORD
  web:
    build: web
    port: 80
    replicas: 2
    health: /up
    env:
      - RAILS_ENV=production
      - POSTGRES_HOST=db
    secrets:
      - POSTGRES_PASSWORD
      - RAILS_MASTER_KEY
    storage:
      - assets
domains:
  web: final.nvoi.to
`

const validEnv = `POSTGRES_PASSWORD=s3cret
RAILS_MASTER_KEY=abc123
`

func pushConfig(t *testing.T, r interface{ ServeHTTP(http.ResponseWriter, *http.Request) }, token, wsID, repoID, yaml, env string) int {
	t.Helper()
	body := map[string]any{
		"compute_provider": "hetzner",
		"dns_provider":     "cloudflare",
		"storage_provider": "cloudflare",
		"build_provider":   "daytona",
		"config":           yaml,
		"env":              env,
	}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestConfig_Push(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	code := pushConfig(t, r, token, wsID, repoID, validYAML, validEnv)
	if code != http.StatusCreated {
		t.Fatalf("push: status = %d, want 201", code)
	}
}

func TestConfig_PushVersionIncrement(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	pushConfig(t, r, token, wsID, repoID, validYAML, validEnv)
	pushConfig(t, r, token, wsID, repoID, validYAML, validEnv)

	// List should return 2 versions.
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/configs", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status = %d", w.Code)
	}

	var configs []struct {
		Version int `json:"version"`
	}
	decode(t, w, &configs)
	if len(configs) != 2 {
		t.Fatalf("got %d configs, want 2", len(configs))
	}
	// Newest first.
	if configs[0].Version != 2 {
		t.Errorf("first version = %d, want 2", configs[0].Version)
	}
	if configs[1].Version != 1 {
		t.Errorf("second version = %d, want 1", configs[1].Version)
	}
}

func TestConfig_GetLatest(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	pushConfig(t, r, token, wsID, repoID, validYAML, validEnv)

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: status = %d, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Version int    `json:"version"`
		Config  string `json:"config"`
		Env     string `json:"env"`
	}
	decode(t, w, &resp)
	if resp.Version != 1 {
		t.Errorf("version = %d, want 1", resp.Version)
	}
	if resp.Config == "" {
		t.Error("config should not be empty")
	}
	// Env hidden by default.
	if resp.Env != "" {
		t.Error("env should be hidden without ?reveal=true")
	}
}

func TestConfig_GetLatestReveal(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	pushConfig(t, r, token, wsID, repoID, validYAML, validEnv)

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config?reveal=true", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get reveal: status = %d", w.Code)
	}

	var resp struct {
		Env string `json:"env"`
	}
	decode(t, w, &resp)
	if resp.Env == "" {
		t.Error("env should be visible with ?reveal=true")
	}
}

func TestConfig_GetNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get empty: status = %d, want 404", w.Code)
	}
}

func TestConfig_PushInvalidYAML(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	code := pushConfig(t, r, token, wsID, repoID, "not: [valid: yaml: {", "")
	if code != http.StatusBadRequest {
		t.Fatalf("invalid yaml: status = %d, want 400", code)
	}
}

func TestConfig_PushValidationErrors(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// No servers, no services.
	code := pushConfig(t, r, token, wsID, repoID, "servers: {}\nservices: {}", "")
	if code != http.StatusBadRequest {
		t.Fatalf("validation: status = %d, want 400", code)
	}
}

func TestConfig_PushMissingSecret(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// Config references POSTGRES_PASSWORD but env is empty.
	yaml := `
servers:
  master:
    type: cx23
    region: fsn1
services:
  db:
    image: postgres:17
    secrets:
      - POSTGRES_PASSWORD
`
	code := pushConfig(t, r, token, wsID, repoID, yaml, "")
	if code != http.StatusBadRequest {
		t.Fatalf("missing secret: status = %d, want 400", code)
	}
}

func TestConfig_Plan(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	pushConfig(t, r, token, wsID, repoID, validYAML, validEnv)

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config/plan", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("plan: status = %d, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Version int `json:"version"`
		Steps   []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"steps"`
	}
	decode(t, w, &resp)

	if resp.Version != 1 {
		t.Errorf("version = %d, want 1", resp.Version)
	}
	if len(resp.Steps) == 0 {
		t.Fatal("steps should not be empty")
	}

	// First step should be instance.set.
	if resp.Steps[0].Kind != "instance.set" {
		t.Errorf("first step = %s, want instance.set", resp.Steps[0].Kind)
	}
	// Last step should be dns.set.
	last := resp.Steps[len(resp.Steps)-1]
	if last.Kind != "dns.set" {
		t.Errorf("last step = %s, want dns.set", last.Kind)
	}
}

func TestConfig_PlanNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config/plan", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("plan empty: status = %d, want 404", w.Code)
	}
}

func TestConfig_PushInvalidComputeProvider(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	body := map[string]any{
		"compute_provider": "digitalocean",
		"config":           validYAML,
		"env":              validEnv,
	}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider: status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
}

func TestConfig_PushInvalidDNSProvider(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	body := map[string]any{
		"compute_provider": "hetzner",
		"dns_provider":     "godaddy",
		"config":           validYAML,
		"env":              validEnv,
	}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid dns provider: status = %d, want 400", w.Code)
	}
}

func TestConfig_PushMissingComputeProvider(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	body := map[string]any{
		"config": validYAML,
		"env":    validEnv,
	}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing compute provider: status = %d, want 400", w.Code)
	}
}

func TestConfig_PushOptionalProviders(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// Only compute_provider is required. Others are optional.
	minimalYAML := `
servers:
  master:
    type: cx23
    region: fsn1
services:
  web:
    image: nginx
    port: 80
`
	body := map[string]any{
		"compute_provider": "hetzner",
		"config":           minimalYAML,
	}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("optional providers: status = %d, want 201, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ComputeProvider string `json:"compute_provider"`
		DNSProvider     string `json:"dns_provider"`
	}
	decode(t, w, &resp)
	if resp.ComputeProvider != "hetzner" {
		t.Errorf("compute_provider = %q, want hetzner", resp.ComputeProvider)
	}
	if resp.DNSProvider != "" {
		t.Errorf("dns_provider = %q, want empty", resp.DNSProvider)
	}
}

func TestConfig_ProvidersInGetResponse(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	pushConfig(t, r, token, wsID, repoID, validYAML, validEnv)

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		ComputeProvider string `json:"compute_provider"`
		DNSProvider     string `json:"dns_provider"`
		StorageProvider string `json:"storage_provider"`
		BuildProvider   string `json:"build_provider"`
	}
	decode(t, w, &resp)
	if resp.ComputeProvider != "hetzner" {
		t.Errorf("compute_provider = %q", resp.ComputeProvider)
	}
	if resp.DNSProvider != "cloudflare" {
		t.Errorf("dns_provider = %q", resp.DNSProvider)
	}
	if resp.StorageProvider != "cloudflare" {
		t.Errorf("storage_provider = %q", resp.StorageProvider)
	}
	if resp.BuildProvider != "daytona" {
		t.Errorf("build_provider = %q", resp.BuildProvider)
	}
}

func TestConfig_CrossUserIsolation(t *testing.T) {
	rA, db := testRouter(t, "alice")
	tokenA, _, wsA := doLogin(t, rA, "alice")
	repoA := createRepo(t, rA, tokenA, wsA, "secret-app")
	pushConfig(t, rA, tokenA, wsA, repoA, validYAML, validEnv)

	rB := newRouterWithDB(db, "bob")
	tokenB, _, _ := doLogin(t, rB, "bob")

	// Bob can't read alice's config.
	req := authRequest("GET", "/workspaces/"+wsA+"/repos/"+repoA+"/config", nil, tokenB)
	w := httptest.NewRecorder()
	rB.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-user: status = %d, want 404", w.Code)
	}
}
