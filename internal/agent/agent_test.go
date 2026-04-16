package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/testutil"
	"k8s.io/client-go/kubernetes/fake"
)

func testAgent(t *testing.T, token string) *Agent {
	t.Helper()
	cfg := &config.AppConfig{
		App: "test", Env: "prod",
		Providers: config.ProvidersDef{Compute: "hetzner"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx21", Region: "fsn1", Role: "master"}},
	}
	kc := kube.NewFromClientset(fake.NewSimpleClientset())
	a, err := New(context.Background(), cfg, AgentOpts{Kube: kc, Token: token})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestAuth_NoToken_AllRequestsPass(t *testing.T) {
	a := testAgent(t, "")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("health without token config should pass, got %d", w.Code)
	}

	// Describe should also pass (no auth header, no token configured).
	req = httptest.NewRequest("GET", "/describe", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Fatal("describe without token config should not return 401")
	}
}

func TestAuth_WithToken_RejectsNoHeader(t *testing.T) {
	a := testAgent(t, "secret-token-123")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	// Health is always open.
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("health should always pass, got %d", w.Code)
	}

	// JSONL endpoint without auth → 401.
	req = httptest.NewRequest("GET", "/describe", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("describe without auth header should be 401, got %d", w.Code)
	}
}

func TestAuth_WithToken_RejectsWrongToken(t *testing.T) {
	a := testAgent(t, "secret-token-123")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/describe", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token should be 401, got %d", w.Code)
	}
}

func TestAuth_WithToken_AcceptsCorrectToken(t *testing.T) {
	a := testAgent(t, "secret-token-123")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/describe", nil)
	req.Header.Set("Authorization", "Bearer secret-token-123")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	// May fail with a kube error (fake client, no real cluster), but NOT 401.
	if w.Code == http.StatusUnauthorized {
		t.Fatal("correct token should not return 401")
	}
}

func TestAuth_ConfigPush_RequiresToken(t *testing.T) {
	a := testAgent(t, "secret-token-123")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	body := `app: test
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cx21
    region: fsn1
    role: master`

	// Without token → 401.
	req := httptest.NewRequest("POST", "/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("config push without token should be 401, got %d", w.Code)
	}

	// With token → accepted.
	req = httptest.NewRequest("POST", "/config", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token-123")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Fatal("config push with correct token should not be 401")
	}
}

func TestAuth_BackupDownload_RequiresToken(t *testing.T) {
	a := testAgent(t, "secret-token-123")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	// Without token → 401.
	req := httptest.NewRequest("GET", "/db/main/backups/some-key", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("backup download without token should be 401, got %d", w.Code)
	}
}

func TestAuth_Deploy_RequiresToken(t *testing.T) {
	a := testAgent(t, "secret-token-123")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	// Without token → 401.
	req := httptest.NewRequest("POST", "/deploy", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("deploy without token should be 401, got %d", w.Code)
	}
}

func TestAuth_Teardown_RequiresToken(t *testing.T) {
	a := testAgent(t, "secret-token-123")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/teardown", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("teardown without token should be 401, got %d", w.Code)
	}
}

func TestAuth_Exec_RequiresToken(t *testing.T) {
	a := testAgent(t, "secret-token-123")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/exec/web", strings.NewReader(`{"command":["sh"]}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("exec without token should be 401, got %d", w.Code)
	}
}

func TestAuth_SQL_RequiresToken(t *testing.T) {
	a := testAgent(t, "secret-token-123")
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/db/main/sql", strings.NewReader(`{"query":"SELECT 1"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("SQL without token should be 401, got %d", w.Code)
	}
}

// ── loadConfig tests ───────────────────────────────────────────────────────

func TestLoadConfig_InvalidConfigRejected(t *testing.T) {
	a := testAgent(t, "")

	// Push config with no servers — ValidateConfig will reject it.
	bad := &config.AppConfig{App: "test", Env: "prod", Providers: config.ProvidersDef{Compute: "hetzner"}}
	err := a.loadConfig(context.Background(), bad)
	if err == nil {
		t.Fatal("expected error for config with no servers")
	}

	// State should be unchanged — still the original config.
	cfg, _ := a.snapshot(nil)
	if cfg.App != "test" {
		t.Fatal("state should be preserved after rejected config push")
	}
	if len(cfg.Servers) == 0 {
		t.Fatal("original state should have servers, but loadConfig overwrote it with invalid config")
	}
}

func TestLoadConfig_ValidConfigSwaps(t *testing.T) {
	a := testAgent(t, "")

	cfg, _ := a.snapshot(nil)
	if cfg.App != "test" {
		t.Fatalf("initial app should be test, got %s", cfg.App)
	}

	// Push valid config with different app name.
	newCfg := &config.AppConfig{
		App: "newapp", Env: "staging",
		Providers: config.ProvidersDef{Compute: "hetzner"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx21", Region: "fsn1", Role: "master"}},
	}
	if err := a.loadConfig(context.Background(), newCfg); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	cfg, _ = a.snapshot(nil)
	if cfg.App != "newapp" || cfg.Env != "staging" {
		t.Fatalf("expected newapp/staging, got %s/%s", cfg.App, cfg.Env)
	}
}

func TestSnapshot_OutputIsPerRequest(t *testing.T) {
	a := testAgent(t, "")

	_, dc1 := a.snapshot(nil)
	out := &testutil.MockOutput{}
	_, dc2 := a.snapshot(out)

	if dc1.Output != nil {
		t.Error("dc1 Output should be nil")
	}
	// dc2 has a non-nil Output, dc1 is unaffected — they're independent copies.
	if dc2.Output == nil {
		t.Error("dc2 Output should be set")
	}
}

func TestNew_FailsOnInvalidConfig(t *testing.T) {
	bad := &config.AppConfig{App: "test", Env: "prod"} // no compute provider
	kc := kube.NewFromClientset(fake.NewSimpleClientset())
	_, err := New(context.Background(), bad, AgentOpts{Kube: kc})
	if err == nil {
		t.Fatal("expected error for config with no compute provider")
	}
}

func TestNew_FailsOnNoServers(t *testing.T) {
	bad := &config.AppConfig{
		App: "test", Env: "prod",
		Providers: config.ProvidersDef{Compute: "hetzner"},
	}
	kc := kube.NewFromClientset(fake.NewSimpleClientset())
	_, err := New(context.Background(), bad, AgentOpts{Kube: kc})
	if err == nil {
		t.Fatal("expected error for config with no servers")
	}
}
