package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaces_List(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, _ := doLogin(t, r, "octocat")

	req := authRequest("GET", "/workspaces", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var workspaces []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decode(t, w, &workspaces)

	if len(workspaces) != 1 {
		t.Fatalf("got %d workspaces, want 1 (default)", len(workspaces))
	}
	if workspaces[0].Name != "default" {
		t.Errorf("name = %q, want default", workspaces[0].Name)
	}
}

func TestWorkspaces_Create(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, _ := doLogin(t, r, "octocat")

	req := authRequest("POST", "/workspaces", map[string]string{"name": "staging"}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", w.Code, w.Body.String())
	}

	var ws struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decode(t, w, &ws)

	if ws.Name != "staging" {
		t.Errorf("name = %q, want staging", ws.Name)
	}
	if ws.ID == "" {
		t.Error("id should not be empty")
	}

	// List should now return 2 workspaces.
	req = authRequest("GET", "/workspaces", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var list []struct{ ID string }
	decode(t, w, &list)
	if len(list) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(list))
	}
}

func TestWorkspaces_Get(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	req := authRequest("GET", "/workspaces/"+wsID, nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var ws struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decode(t, w, &ws)
	if ws.ID != wsID {
		t.Errorf("id = %q, want %q", ws.ID, wsID)
	}
}

func TestWorkspaces_Update(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	req := authRequest("PUT", "/workspaces/"+wsID, map[string]string{"name": "production"}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var ws struct{ Name string }
	decode(t, w, &ws)
	if ws.Name != "production" {
		t.Errorf("name = %q, want production", ws.Name)
	}
}

func TestWorkspaces_Delete(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	req := authRequest("DELETE", "/workspaces/"+wsID, nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	// Should be gone.
	req = authRequest("GET", "/workspaces/"+wsID, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 after delete", w.Code)
	}
}

func TestWorkspaces_NotFoundForOtherUser(t *testing.T) {
	// User A creates workspace, User B can't see it.
	rA, db := testRouter(t, "alice")
	tokenA, _, wsID := doLogin(t, rA, "alice")

	// Verify alice can see it.
	req := authRequest("GET", "/workspaces/"+wsID, nil, tokenA)
	w := httptest.NewRecorder()
	rA.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("alice: status = %d, want 200", w.Code)
	}

	// Login as bob on the same DB.
	rB := newRouterWithDB(db, "bob")
	tokenB, _, _ := doLogin(t, rB, "bob")

	// Bob can't see alice's workspace.
	req = authRequest("GET", "/workspaces/"+wsID, nil, tokenB)
	w = httptest.NewRecorder()
	rB.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bob: status = %d, want 404", w.Code)
	}
}

func TestWorkspaces_CreateMissingName(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, _ := doLogin(t, r, "octocat")

	req := authRequest("POST", "/workspaces", map[string]string{}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
