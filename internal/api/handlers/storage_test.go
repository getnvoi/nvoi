package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	// StorageList is config-derived — no SSH needed. With no config on repo,
	// returns 200 with empty list (no storage names to resolve).
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/storage", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty list)", w.Code)
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

func TestStorageEmpty_NoProvider(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/storage/assets/empty", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (no storage provider)", w.Code)
	}
}
