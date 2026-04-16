package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Config loading ──────────────────────────────────────────────────────────

func TestDeployWithoutConfig(t *testing.T) {
	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--config", "/nonexistent/nvoi.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("error = %q, want file-not-found error", err.Error())
	}
}

// ── Bootstrap: no master, prompt declined ───────────────────────────────────

func TestDeploy_NoMaster_Declined(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\nproviders:\n  compute: hetzner\nservers:\n  master:\n    type: cx23\n    region: fsn1\n    role: master\nservices:\n  web:\n    image: nginx\n"), 0o644)

	t.Setenv("HOME", dir) // no SSH key → ErrNoMaster

	// Pipe "n" to stdin to decline the prompt.
	r, w, _ := os.Pipe()
	w.WriteString("n\n")
	w.Close()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--config", cfgPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("error = %q, want aborted", err.Error())
	}
}

// ── Bootstrap: no master, -y flag → local deploy ────────────────────────────

func TestDeploy_NoMaster_AutoConfirm(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	t.Setenv("HOME", dir) // no SSH key → ErrNoMaster

	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--config", cfgPath, "-y"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error (no compute provider)")
	}
	// Should reach the local bootstrap path → hits validation
	if !strings.Contains(err.Error(), "providers.compute is required") {
		t.Fatalf("error = %q, want validation error from local bootstrap", err.Error())
	}
}

// ── Agent reachable → commands go through agent ─────────────────────────────

func TestDeploy_AgentReachable(t *testing.T) {
	// Start a mock agent server.
	var gotPath, gotMethod string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/x-ndjson")
		// Simulate a deploy that fails validation (no real cluster).
		json.NewEncoder(w).Encode(map[string]string{"type": "error", "message": "providers.compute is required"})
	}))
	defer agent.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	// Test agentBackend directly against the mock server.
	ab := &agentBackend{
		client:     &http.Client{},
		baseURL:    agent.URL,
		out:        resolveOutput(rootCmd()),
		configPath: cfgPath,
	}
	err := ab.Deploy(t.Context())
	// The mock returns an error event, which streamCommand surfaces.
	if err == nil {
		t.Fatal("expected error from agent")
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	// Config push + deploy = two requests. gotPath is the last one.
	if gotPath != "/deploy" {
		t.Fatalf("path = %q, want /deploy", gotPath)
	}
}

func TestDescribe_AgentReachable(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"services": []any{}, "pods": []any{}})
	}))
	defer agent.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	ab := &agentBackend{
		client:     &http.Client{},
		baseURL:    agent.URL,
		out:        resolveOutput(rootCmd()),
		configPath: cfgPath,
	}
	err := ab.Describe(t.Context(), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTeardown_AgentReachable(t *testing.T) {
	var gotBody map[string]any
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/teardown" {
			json.NewDecoder(r.Body).Decode(&gotBody)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(200)
	}))
	defer agent.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	ab := &agentBackend{
		client:     &http.Client{},
		baseURL:    agent.URL,
		out:        resolveOutput(rootCmd()),
		configPath: cfgPath,
	}
	ab.Teardown(t.Context(), true, true)
	if gotBody["delete_volumes"] != true {
		t.Fatalf("delete_volumes = %v, want true", gotBody["delete_volumes"])
	}
	if gotBody["delete_storage"] != true {
		t.Fatalf("delete_storage = %v, want true", gotBody["delete_storage"])
	}
}

func TestCronRun_AgentReachable(t *testing.T) {
	var gotPath string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(200)
	}))
	defer agent.Close()

	ab := &agentBackend{
		client:  &http.Client{},
		baseURL: agent.URL,
		out:     resolveOutput(rootCmd()),
	}
	ab.CronRun(t.Context(), "cleanup")
	if gotPath != "/cron/cleanup/run" {
		t.Fatalf("path = %q, want /cron/cleanup/run", gotPath)
	}
}

func TestExec_AgentReachable(t *testing.T) {
	var gotPath, gotMethod string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(200)
	}))
	defer agent.Close()

	ab := &agentBackend{
		client:  &http.Client{},
		baseURL: agent.URL,
		out:     resolveOutput(rootCmd()),
	}
	ab.Exec(t.Context(), "web", []string{"sh"})
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/exec/web" {
		t.Fatalf("path = %q, want /exec/web", gotPath)
	}
}

func TestSSH_AgentReachable(t *testing.T) {
	var gotPath, gotMethod string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(200)
	}))
	defer agent.Close()

	ab := &agentBackend{
		client:  &http.Client{},
		baseURL: agent.URL,
		out:     resolveOutput(rootCmd()),
	}
	ab.SSH(t.Context(), []string{"kubectl", "get", "pods"})
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/ssh" {
		t.Fatalf("path = %q, want /ssh", gotPath)
	}
}

func TestLogs_AgentReachable(t *testing.T) {
	var gotPath string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(200)
	}))
	defer agent.Close()

	ab := &agentBackend{
		client:  &http.Client{},
		baseURL: agent.URL,
		out:     resolveOutput(rootCmd()),
	}
	ab.Logs(t.Context(), LogsOpts{Service: "web", Follow: true, Tail: 100})
	if !strings.Contains(gotPath, "/logs/web") {
		t.Fatalf("path = %q, want /logs/web", gotPath)
	}
	if !strings.Contains(gotPath, "follow=true") {
		t.Fatalf("path = %q, want follow=true", gotPath)
	}
	if !strings.Contains(gotPath, "tail=100") {
		t.Fatalf("path = %q, want tail=100", gotPath)
	}
}

func TestDatabaseSQL_AgentReachable(t *testing.T) {
	var gotPath, gotMethod string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("1 row"))
	}))
	defer agent.Close()

	ab := &agentBackend{
		client:  &http.Client{},
		baseURL: agent.URL,
		out:     resolveOutput(rootCmd()),
	}
	ab.DatabaseSQL(t.Context(), "main", "postgres", "SELECT 1")
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/db/main/sql" {
		t.Fatalf("path = %q, want /db/main/sql", gotPath)
	}
}
