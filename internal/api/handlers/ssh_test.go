package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSSH_RequiresAuth(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	body := map[string]any{"command": []string{"echo", "hi"}}
	req := authRequest("POST", "/workspaces/fake/repos/fake/ssh", body, "bad-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestSSH_RequiresCommand(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/ssh", map[string]any{}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422, body: %s", w.Code, w.Body.String())
	}
}

func TestSSH_RepoNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	body := map[string]any{"command": []string{"echo", "hi"}}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/00000000-0000-0000-0000-000000000000/ssh", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
