package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func createRepo(t *testing.T, r interface{ ServeHTTP(http.ResponseWriter, *http.Request) }, token, wsID, name string) string {
	t.Helper()
	req := authRequest("POST", "/workspaces/"+wsID+"/repos", map[string]string{"name": name}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create repo: status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp struct{ ID string }
	decode(t, w, &resp)
	return resp.ID
}

func TestRepos_CRUD(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	// Create
	repoID := createRepo(t, r, token, wsID, "my-app")

	// List
	req := authRequest("GET", "/workspaces/"+wsID+"/repos", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status = %d", w.Code)
	}
	var repos []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decode(t, w, &repos)
	if len(repos) != 1 {
		t.Fatalf("got %d repos, want 1", len(repos))
	}
	if repos[0].Name != "my-app" {
		t.Errorf("name = %q, want my-app", repos[0].Name)
	}

	// Get
	req = authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: status = %d", w.Code)
	}

	// Update
	req = authRequest("PUT", "/workspaces/"+wsID+"/repos/"+repoID, map[string]string{"name": "renamed"}, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d, body: %s", w.Code, w.Body.String())
	}
	var updated struct{ Name string }
	decode(t, w, &updated)
	if updated.Name != "renamed" {
		t.Errorf("name after update = %q, want renamed", updated.Name)
	}

	// Delete
	req = authRequest("DELETE", "/workspaces/"+wsID+"/repos/"+repoID, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d", w.Code)
	}

	// Verify gone
	req = authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("after delete: status = %d, want 404", w.Code)
	}
}

func TestRepos_ScopedToWorkspace(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	// Create a second workspace.
	req := authRequest("POST", "/workspaces", map[string]string{"name": "other"}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var ws2 struct{ ID string }
	decode(t, w, &ws2)

	// Create repo in workspace 1.
	repoID := createRepo(t, r, token, wsID, "app-1")

	// Can't access that repo through workspace 2.
	req = authRequest("GET", "/workspaces/"+ws2.ID+"/repos/"+repoID, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace: status = %d, want 404", w.Code)
	}
}

func TestRepos_OtherUserCantAccess(t *testing.T) {
	rA, db := testRouter(t, "alice")
	tokenA, _, wsA := doLogin(t, rA, "alice")
	createRepo(t, rA, tokenA, wsA, "secret-repo")

	rB := newRouterWithDB(db, "bob")
	tokenB, _, _ := doLogin(t, rB, "bob")

	// Bob can't list repos in alice's workspace.
	req := authRequest("GET", "/workspaces/"+wsA+"/repos", nil, tokenB)
	w := httptest.NewRecorder()
	rB.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bob listing alice repos: status = %d, want 404", w.Code)
	}
}

func TestRepos_CreateMissingName(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	req := authRequest("POST", "/workspaces/"+wsID+"/repos", map[string]string{}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
