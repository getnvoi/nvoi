package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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

	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/services/web/exec", map[string]any{}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (missing command)", w.Code)
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
