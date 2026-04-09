package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDescribe_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/describe", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestDescribe_RepoNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/00000000-0000-0000-0000-000000000000/describe", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDescribe_NoProvider(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// No compute provider linked — describe should fail (can't resolve compute provider).
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/describe", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (no compute provider)", w.Code)
	}
}

func TestResources_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	req := httptest.NewRequest("GET", "/workspaces/fake/repos/fake/resources", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestResources_RepoNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/00000000-0000-0000-0000-000000000000/resources", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestResources_NoProvider(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// No compute provider linked — resources returns empty (no providers to query).
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/resources", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// With no providers, Resources returns empty groups (200) or 500 depending on
	// whether provider.Resolve fails. Accept either — the point is it doesn't 400.
	if w.Code == http.StatusBadRequest {
		t.Fatalf("status = 400, should not require config push anymore")
	}
}
