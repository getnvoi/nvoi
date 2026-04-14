package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// validConfig passes ValidateConfig (app+env injected from repo by handler).
const validConfig = `providers:
  compute: hetzner
servers:
  master:
    type: cax11
    region: nbg1
    role: master
services:
  web:
    image: nginx
    port: 80
`

func TestConfigShow_Empty(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "myapp")

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct{ Config string }
	decode(t, w, &body)
	if body.Config != "" {
		t.Fatalf("config = %q, want empty", body.Config)
	}
}

func TestConfigSave_Valid(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "myapp")
	base := "/workspaces/" + wsID + "/repos/" + repoID + "/config"

	req := authRequest("PUT", base, map[string]string{"config": validConfig}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var body struct {
		Config   string
		Warnings []string
	}
	decode(t, w, &body)
	if !strings.Contains(body.Config, "app: myapp") {
		t.Fatalf("config should contain injected app, got:\n%s", body.Config)
	}
	if len(body.Warnings) != 0 {
		t.Fatalf("expected no warnings for valid config, got: %v", body.Warnings)
	}
}

func TestConfigSave_PartialConfig_SavesWithWarnings(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "myapp")
	base := "/workspaces/" + wsID + "/repos/" + repoID + "/config"

	// Only providers, no servers — incomplete but saves.
	yaml := "providers:\n  compute: hetzner\n"
	req := authRequest("PUT", base, map[string]string{"config": yaml}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (save always succeeds), body: %s", w.Code, w.Body.String())
	}
	var body struct {
		Config   string
		Warnings []string
	}
	decode(t, w, &body)
	if len(body.Warnings) == 0 {
		t.Fatal("expected warnings for incomplete config")
	}
	if !strings.Contains(body.Warnings[0], "server") {
		t.Fatalf("warning should mention server, got: %v", body.Warnings)
	}
}

func TestConfigSave_InvalidYAML_Rejected(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "myapp")
	base := "/workspaces/" + wsID + "/repos/" + repoID + "/config"

	req := authRequest("PUT", base, map[string]string{"config": "{{invalid"}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed YAML", w.Code)
	}
}

func TestConfigSave_PersistsAcrossReads(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "myapp")
	base := "/workspaces/" + wsID + "/repos/" + repoID + "/config"

	req := authRequest("PUT", base, map[string]string{"config": validConfig}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save: status = %d", w.Code)
	}

	req = authRequest("GET", base, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("show: status = %d", w.Code)
	}
	var body struct{ Config string }
	decode(t, w, &body)
	if !strings.Contains(body.Config, "cax11") {
		t.Fatalf("config should persist, got:\n%s", body.Config)
	}
}

func TestConfigSave_Overwrite(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "myapp")
	base := "/workspaces/" + wsID + "/repos/" + repoID + "/config"

	req := authRequest("PUT", base, map[string]string{"config": validConfig}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first: status = %d", w.Code)
	}

	updated := strings.Replace(validConfig, "nginx", "caddy", 1)
	req = authRequest("PUT", base, map[string]string{"config": updated}, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("overwrite: status = %d", w.Code)
	}

	req = authRequest("GET", base, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body struct{ Config string }
	decode(t, w, &body)
	if !strings.Contains(body.Config, "caddy") {
		t.Fatalf("should contain caddy, got:\n%s", body.Config)
	}
}

func TestConfigShow_Unauthorized(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "myapp")
	_ = json.NewEncoder(nil)

	req := httptest.NewRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("expected auth failure")
	}
}

func TestConfigShow_WarningsOnInvalidStored(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "myapp")
	base := "/workspaces/" + wsID + "/repos/" + repoID + "/config"

	// Save partial config (saves with warnings).
	req := authRequest("PUT", base, map[string]string{"config": "providers:\n  compute: hetzner\n"}, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save: status = %d", w.Code)
	}

	// Show should include warnings.
	req = authRequest("GET", base, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body struct {
		Config   string
		Warnings []string
	}
	decode(t, w, &body)
	if len(body.Warnings) == 0 {
		t.Fatal("show should include validation warnings for incomplete config")
	}
}
