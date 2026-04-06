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

func TestDescribe_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/describe", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (no config pushed)", w.Code)
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

func TestResources_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/resources", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (no config pushed)", w.Code)
	}
}
