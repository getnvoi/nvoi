package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestBuilds_NoProvider(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/builds", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (no compute provider)", w.Code)
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

func TestBuildLatest_NoProvider(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/builds/web/latest", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (no compute provider)", w.Code)
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

func TestBuildPrune_MissingKeep(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/builds/web/prune", map[string]any{}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (missing keep)", w.Code)
	}
}
