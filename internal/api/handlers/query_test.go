package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ── Instances ────────────────────────────────────────────────────────────────

func TestInstances_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/instances", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestInstances_RepoNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/00000000-0000-0000-0000-000000000000/instances", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestInstances_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/instances", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ── Volumes ──────────────────────────────────────────────────────────────────

func TestVolumes_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/volumes", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestVolumes_RepoNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/00000000-0000-0000-0000-000000000000/volumes", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestVolumes_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/volumes", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ── DNS ──────────────────────────────────────────────────────────────────────

func TestDNS_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/dns", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestDNS_RepoNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/00000000-0000-0000-0000-000000000000/dns", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDNS_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/dns", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDNS_NoDNSProvider(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// Push config with no dns_provider.
	minimalYAML := "servers:\n  master:\n    type: cx23\n    region: fsn1\nservices:\n  web:\n    image: nginx\n    port: 80\n"
	body := map[string]any{"compute_provider": "hetzner", "config": minimalYAML}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("push: status = %d, body: %s", w.Code, w.Body.String())
	}

	req = authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/dns", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (no dns provider)", w.Code)
	}
}

// ── Secrets ──────────────────────────────────────────────────────────────────

func TestSecrets_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/secrets", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestSecrets_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/secrets", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ── Storage ──────────────────────────────────────────────────────────────────

func TestStorage_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/storage", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestStorage_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/storage", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestStorageEmpty_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("POST", "/workspaces/fake/repos/fake/storage/assets/empty", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestStorageEmpty_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/storage/assets/empty", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ── Builds ───────────────────────────────────────────────────────────────────

func TestBuilds_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/builds", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestBuilds_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/builds", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestBuildLatest_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/builds/web/latest", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestBuildLatest_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/builds/web/latest", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestBuildPrune_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("POST", "/workspaces/fake/repos/fake/builds/web/prune", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestBuildPrune_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/builds/web/prune", map[string]any{"keep": 3}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestBuildPrune_MissingKeep(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// Push config so we get past the no-config check.
	minimalYAML := "servers:\n  master:\n    type: cx23\n    region: fsn1\nservices:\n  web:\n    image: nginx\n    port: 80\n"
	body := map[string]any{"compute_provider": "hetzner", "config": minimalYAML}
	pushReq := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	pw := httptest.NewRecorder()
	r.ServeHTTP(pw, pushReq)

	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/builds/web/prune", map[string]any{}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (missing keep)", w.Code)
	}
}

// ── Service Logs ─────────────────────────────────────────────────────────────

func TestServiceLogs_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/services/web/logs", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestServiceLogs_RepoNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/00000000-0000-0000-0000-000000000000/services/web/logs", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestServiceLogs_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/services/web/logs", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ── Exec ─────────────────────────────────────────────────────────────────────

func TestExec_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	body := map[string]any{"command": []string{"echo", "hi"}}
	req := authRequest("POST", "/workspaces/fake/repos/fake/services/web/exec", body, "bad-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestExec_RepoNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	body := map[string]any{"command": []string{"echo", "hi"}}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/00000000-0000-0000-0000-000000000000/services/web/exec", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestExec_MissingCommand(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// Push config so we get past loadRepo.
	minimalYAML := "servers:\n  master:\n    type: cx23\n    region: fsn1\nservices:\n  web:\n    image: nginx\n    port: 80\n"
	body := map[string]any{"compute_provider": "hetzner", "config": minimalYAML}
	pushReq := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	pw := httptest.NewRecorder()
	r.ServeHTTP(pw, pushReq)

	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/services/web/exec", map[string]any{}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (missing command)", w.Code)
	}
}

func TestExec_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	body := map[string]any{"command": []string{"echo", "hi"}}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/services/web/exec", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ── Cross-user isolation (spot check) ────────────────────────────────────────

func TestQueryEndpoints_CrossUserIsolation(t *testing.T) {
	rA, db := testRouter(t, "alice")
	tokenA, _, wsA := doLogin(t, rA, "alice")
	repoA := createRepo(t, rA, tokenA, wsA, "secret-app")

	rB := newRouterWithDB(db, "bob")
	tokenB, _, _ := doLogin(t, rB, "bob")

	endpoints := []struct {
		method, path string
	}{
		{"GET", "/workspaces/" + wsA + "/repos/" + repoA + "/instances"},
		{"GET", "/workspaces/" + wsA + "/repos/" + repoA + "/volumes"},
		{"GET", "/workspaces/" + wsA + "/repos/" + repoA + "/dns"},
		{"GET", "/workspaces/" + wsA + "/repos/" + repoA + "/secrets"},
		{"GET", "/workspaces/" + wsA + "/repos/" + repoA + "/storage"},
		{"GET", "/workspaces/" + wsA + "/repos/" + repoA + "/builds"},
		{"GET", "/workspaces/" + wsA + "/repos/" + repoA + "/services/web/logs"},
	}

	for _, ep := range endpoints {
		req := authRequest(ep.method, ep.path, nil, tokenB)
		w := httptest.NewRecorder()
		rB.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: bob got %d, want 404", ep.method, ep.path, w.Code)
		}
	}
}
