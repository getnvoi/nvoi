package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	// SecretList is config-driven — no SSH needed. With no config on repo,
	// returns 200 with empty list.
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/secrets", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty list)", w.Code)
	}
}
