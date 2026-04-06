package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getnvoi/nvoi/internal/api"
)

func TestLogin_NewUser(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	body, _ := json.Marshal(map[string]string{"github_token": "fake"})
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Token     string        `json:"token"`
		User      api.User      `json:"user"`
		Workspace api.Workspace `json:"workspace"`
		IsNew     bool          `json:"is_new"`
	}
	decode(t, w, &resp)

	if resp.Token == "" {
		t.Error("token is empty")
	}
	if resp.User.GithubUsername != "octocat" {
		t.Errorf("username = %q, want octocat", resp.User.GithubUsername)
	}
	if !resp.IsNew {
		t.Error("is_new should be true for first login")
	}
	if resp.Workspace.ID == "" {
		t.Error("default workspace should be created")
	}
	if resp.Workspace.Name != "default" {
		t.Errorf("workspace name = %q, want default", resp.Workspace.Name)
	}
}

func TestLogin_ExistingUser(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	// First login — creates user.
	token1, _, _ := doLogin(t, r, "octocat")

	// Second login — finds existing user.
	body, _ := json.Marshal(map[string]string{"github_token": "fake"})
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Token string `json:"token"`
		IsNew bool   `json:"is_new"`
	}
	decode(t, w, &resp)

	if resp.IsNew {
		t.Error("is_new should be false for existing user")
	}
	_ = token1 // both logins succeed — tokens may match within the same second
	if resp.Token == "" {
		t.Error("token is empty")
	}
}

func TestLogin_MissingToken(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	req := httptest.NewRequest("POST", "/login", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
}

func TestAuth_NoHeader(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	req := httptest.NewRequest("GET", "/workspaces", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAuth_InvalidToken(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	req := httptest.NewRequest("GET", "/workspaces", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestHealth(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct{ Status string }
	decode(t, w, &resp)
	if resp.Status != "ok" {
		t.Errorf("status = %q, want ok", resp.Status)
	}
}

func TestDocs_UI(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	req := httptest.NewRequest("GET", "/docs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("docs UI: status = %d, want 200", w.Code)
	}
}

func TestOpenAPI_Spec(t *testing.T) {
	r, _ := testRouter(t, "octocat")

	req := httptest.NewRequest("GET", "/openapi.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("openapi: status = %d, want 200", w.Code)
	}
}
