package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/internal/api"
)

// createRepoGetToken creates a repo via the API and returns (repoID, agentToken).
func createRepoGetToken(t *testing.T, r http.Handler, jwtToken, wsID, name string) (string, string) {
	t.Helper()
	req := authRequest("POST", "/workspaces/"+wsID+"/repos", map[string]string{"name": name}, jwtToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create repo: status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID         string `json:"id"`
		AgentToken string `json:"agent_token"`
	}
	decode(t, w, &resp)
	return resp.ID, resp.AgentToken
}

func postAgentEvents(r http.Handler, agentToken, app, env string, events []map[string]any) *httptest.ResponseRecorder {
	payload := map[string]any{"app": app, "env": env, "events": events}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/agent/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if agentToken != "" {
		req.Header.Set("Authorization", "Bearer "+agentToken)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── CreateRepo returns agent token ──────────────────────────────────────────

func TestCreateRepo_ReturnsAgentToken(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	_, agentToken := createRepoGetToken(t, r, token, wsID, "my-app")
	if agentToken == "" {
		t.Fatal("agent_token should be returned on repo creation")
	}
	if len(agentToken) != 64 { // 32 bytes hex-encoded
		t.Errorf("agent_token length = %d, want 64", len(agentToken))
	}
}

func TestCreateRepo_AgentTokenNotInGetResponse(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")

	repoID, _ := createRepoGetToken(t, r, token, wsID, "my-app")

	// GET should NOT include the token — it's one-time only.
	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID, nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: status = %d", w.Code)
	}
	body := w.Body.String()
	if bytes.Contains([]byte(body), []byte("agent_token")) {
		t.Error("agent_token should not appear in GET response")
	}
}

// ── Agent auth: lookup by hash ──────────────────────────────────────────────

func TestAgentEvents_ValidToken(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	_, agentToken := createRepoGetToken(t, r, token, wsID, "my-app")

	events := []map[string]any{
		{"type": "success", "message": "server created"},
	}
	w := postAgentEvents(r, agentToken, "my-app", "production", events)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp struct{ Stored int }
	decode(t, w, &resp)
	if resp.Stored != 1 {
		t.Errorf("stored = %d, want 1", resp.Stored)
	}
}

func TestAgentEvents_InvalidToken(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	createRepoGetToken(t, r, token, wsID, "my-app")

	events := []map[string]any{
		{"type": "success", "message": "hello"},
	}
	w := postAgentEvents(r, "wrong-token", "my-app", "production", events)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAgentEvents_MissingToken(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	events := []map[string]any{{"type": "success", "message": "hello"}}
	w := postAgentEvents(r, "", "my-app", "production", events)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAgentEvents_WrongAppEnv(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	_, agentToken := createRepoGetToken(t, r, token, wsID, "my-app")

	// Token is valid but app/env don't match.
	events := []map[string]any{{"type": "success", "message": "hello"}}
	w := postAgentEvents(r, agentToken, "wrong-app", "production", events)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for wrong app", w.Code)
	}
}

func TestAgentEvents_CrossWorkspaceIsolation(t *testing.T) {
	// Two workspaces with repos named identically — each should only accept its own token.
	rA, db := testRouter(t, "alice")
	tokenA, _, wsA := doLogin(t, rA, "alice")
	_, agentTokenA := createRepoGetToken(t, rA, tokenA, wsA, "shared-name")

	rB := newRouterWithDB(db, "bob")
	tokenB, _, wsB := doLogin(t, rB, "bob")
	_, agentTokenB := createRepoGetToken(t, rB, tokenB, wsB, "shared-name")

	events := []map[string]any{{"type": "success", "message": "hello"}}

	// Alice's token works for her repo.
	w := postAgentEvents(rA, agentTokenA, "shared-name", "production", events)
	if w.Code != http.StatusOK {
		t.Fatalf("alice token: status = %d, body: %s", w.Code, w.Body.String())
	}

	// Bob's token works for his repo.
	w = postAgentEvents(rB, agentTokenB, "shared-name", "production", events)
	if w.Code != http.StatusOK {
		t.Fatalf("bob token: status = %d, body: %s", w.Code, w.Body.String())
	}

	// Alice's token doesn't work as Bob's.
	if agentTokenA == agentTokenB {
		t.Fatal("tokens should be different")
	}
}

// ── Non-Bearer prefix rejected ──────────────────────────────────────────────

func TestAgentEvents_NonBearerPrefix(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	_, agentToken := createRepoGetToken(t, r, token, wsID, "my-app")

	// Send with wrong prefix — should be rejected.
	payload := map[string]any{
		"app": "my-app", "env": "production",
		"events": []map[string]any{{"type": "success", "message": "hello"}},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/agent/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Custom "+agentToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("non-Bearer prefix: status = %d, want 401", w.Code)
	}
}

// ── Extra field preserved ───────────────────────────────────────────────────

func TestAgentEvents_ExtraFieldStored(t *testing.T) {
	r, db := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	_, agentToken := createRepoGetToken(t, r, token, wsID, "my-app")

	events := []map[string]any{
		{
			"type":    "command",
			"command": "server",
			"action":  "create",
			"name":    "master",
			"extra":   map[string]any{"type": "cax11", "region": "nbg1"},
		},
	}
	w := postAgentEvents(r, agentToken, "my-app", "production", events)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	// Verify the Extra field was stored in the DB.
	var stored api.AgentEvent
	if err := db.First(&stored).Error; err != nil {
		t.Fatalf("query stored event: %v", err)
	}
	if stored.Extra == "" {
		t.Fatal("Extra field should be stored")
	}
	var extra map[string]string
	if err := json.Unmarshal([]byte(stored.Extra), &extra); err != nil {
		t.Fatalf("parse stored Extra: %v", err)
	}
	if extra["region"] != "nbg1" {
		t.Errorf("Extra[region] = %q, want nbg1", extra["region"])
	}
}

// ── Timestamp preserved ─────────────────────────────────────────────────────

func TestAgentEvents_TimestampPreserved(t *testing.T) {
	r, db := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	_, agentToken := createRepoGetToken(t, r, token, wsID, "my-app")

	agentTs := time.Date(2026, 4, 16, 10, 30, 0, 0, time.UTC)
	events := []map[string]any{
		{
			"type":    "success",
			"message": "server created",
			"ts":      agentTs.Format(time.RFC3339),
		},
	}
	w := postAgentEvents(r, agentToken, "my-app", "production", events)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	var stored api.AgentEvent
	db.First(&stored)
	// CreatedAt should use the agent-side timestamp, not server time.
	if stored.CreatedAt.Year() != 2026 || stored.CreatedAt.Month() != 4 || stored.CreatedAt.Day() != 16 {
		t.Errorf("CreatedAt = %v, want 2026-04-16", stored.CreatedAt)
	}
}
