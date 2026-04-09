package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validYAML = `
servers:
  master:
    type: cx23
    region: fsn1
firewall: default
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

func pushConfig(t *testing.T, r interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, token, wsID, repoID, yaml, env string) int {
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

	// Server missing type — should fail validation.
	code := pushConfig(t, r, token, wsID, repoID, "servers:\n  master:\n    region: fsn1\nservices:\n  web:\n    image: nginx\n    port: 80\n", "")
	if code != http.StatusBadRequest {
		t.Fatalf("validation: status = %d, want 400", code)
	}
}

func TestConfig_PushEmptyConfigForDestroy(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// Empty config is valid — used for destroy-via-diff.
	code := pushConfig(t, r, token, wsID, repoID, "servers: {}\nservices: {}", "")
	if code != http.StatusCreated {
		t.Fatalf("empty config push: status = %d, want 201", code)
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
	// Last step should be ingress.apply.
	last := resp.Steps[len(resp.Steps)-1]
	if last.Kind != "ingress.apply" {
		t.Errorf("last step = %s, want ingress.apply", last.Kind)
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
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid provider: status = %d, want 422, body: %s", w.Code, w.Body.String())
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
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid dns provider: status = %d, want 422", w.Code)
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
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing compute provider: status = %d, want 422", w.Code)
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

// ── Managed services ───────────────────────────────────────────────────────────

const managedYAML = `
servers:
  master:
    type: cx23
    region: fsn1
services:
  db:
    managed: postgres
  coder:
    managed: claude
  web:
    image: nginx
    port: 80
    uses: [db, coder]
`

const managedEnvStr = `POSTGRES_PASSWORD=s3cret
POSTGRES_USER=a1b2c3d4
POSTGRES_DB=e5f6a7b8
NVOI_AGENT_TOKEN=tok123
`

func pushManagedConfig(t *testing.T, r interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, token, wsID, repoID string) (int, string) {
	t.Helper()
	body := map[string]any{
		"compute_provider": "hetzner",
		"config":           managedYAML,
		"env":              managedEnvStr,
	}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func TestConfig_ManagedPushRequiresCredentials(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// Push managed config without credentials — should fail.
	body := map[string]any{
		"compute_provider": "hetzner",
		"config":           managedYAML,
	}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing creds: status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing required credential") {
		t.Errorf("error should mention missing credential, got: %s", w.Body.String())
	}
}

func TestConfig_ManagedPlanIncludesExpandedServices(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	pushManagedConfig(t, r, token, wsID, repoID)

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config/plan", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("plan: status = %d, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Steps []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"steps"`
	}
	decode(t, w, &resp)

	// Should have service.set for db, coder, and web exactly once each.
	serviceCounts := map[string]int{}
	for _, s := range resp.Steps {
		if s.Kind == "service.set" {
			serviceCounts[s.Name]++
		}
	}
	for _, want := range []string{"db", "coder", "web"} {
		if serviceCounts[want] != 1 {
			t.Errorf("service.set count for %q = %d, want 1", want, serviceCounts[want])
		}
	}

	volumeCounts := map[string]int{}
	for _, s := range resp.Steps {
		if s.Kind == "volume.set" {
			volumeCounts[s.Name]++
		}
	}
	if volumeCounts["db-data"] != 1 {
		t.Errorf("volume.set count for db-data = %d, want 1", volumeCounts["db-data"])
	}
	if volumeCounts["coder-data"] != 1 {
		t.Errorf("volume.set count for coder-data = %d, want 1", volumeCounts["coder-data"])
	}

	for _, want := range []string{"db", "coder", "web"} {
		if serviceCounts[want] == 0 {
			t.Errorf("missing service.set for %q in plan", want)
		}
	}

	// Should have secret.set steps for managed creds — namespaced.
	secretNames := map[string]bool{}
	for _, s := range resp.Steps {
		if s.Kind == "secret.set" {
			secretNames[s.Name] = true
		}
	}
	// Postgres password is namespaced: POSTGRES_PASSWORD_DB (not bare POSTGRES_PASSWORD).
	if !secretNames["POSTGRES_PASSWORD_DB"] {
		t.Errorf("missing secret.set for POSTGRES_PASSWORD_DB, got: %v", secretNames)
	}
}

func TestConfig_ManagedMultipleSameKind(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	multiYAML := `
servers:
  master:
    type: cx23
    region: fsn1
services:
  db:
    managed: postgres
  analytics:
    managed: postgres
  web:
    image: nginx
    port: 80
    uses: [db, analytics]
`
	body := map[string]any{
		"compute_provider": "hetzner",
		"config":           multiYAML,
		"env":              "POSTGRES_PASSWORD=s3cret\nPOSTGRES_USER=a1b2c3d4\nPOSTGRES_DB=e5f6a7b8\n",
	}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("push multi: status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestConfig_ManagedPlanWebGetsAllInjectedSecrets(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	pushManagedConfig(t, r, token, wsID, repoID)

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config/plan", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Steps []struct {
			Kind   string         `json:"kind"`
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		} `json:"steps"`
	}
	decode(t, w, &resp)

	// Find the web service step and check its secrets include injected creds.
	for _, s := range resp.Steps {
		if s.Kind == "service.set" && s.Name == "web" {
			secrets, ok := s.Params["secrets"]
			if !ok {
				t.Fatal("web service.set has no secrets param")
			}
			secretList, ok := secrets.([]any)
			if !ok {
				t.Fatalf("secrets is %T, want []any", secrets)
			}
			hasDB := false
			hasAgent := false
			for _, s := range secretList {
				str, _ := s.(string)
				if strings.HasPrefix(str, "DATABASE_DB_") {
					hasDB = true
				}
				if strings.HasPrefix(str, "AGENT_CODER_") {
					hasAgent = true
				}
			}
			if !hasDB {
				t.Error("web secrets missing DATABASE_DB_* from uses: [db]")
			}
			if !hasAgent {
				t.Error("web secrets missing AGENT_CODER_* from uses: [coder]")
			}
			return
		}
	}
	t.Fatal("web service.set step not found in plan")
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
