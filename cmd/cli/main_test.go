package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/cli"
	"github.com/getnvoi/nvoi/internal/core"
	"github.com/spf13/cobra"
)

// ── Mode detection ──────────────────────────────────────────────────────────

func TestLocalFlagWithoutConfig(t *testing.T) {
	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--local", "--config", "/nonexistent/nvoi.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("error = %q, want file-not-found error", err.Error())
	}
}

func TestCloudModeWithoutAuth(t *testing.T) {
	cli.ResetAuthCache()
	t.Setenv("HOME", t.TempDir())

	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--config", "/nonexistent/nvoi.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nvoi login") {
		t.Fatalf("error = %q, want login suggestion", err.Error())
	}
}

func TestCloudModeWithoutAuthWithConfig(t *testing.T) {
	cli.ResetAuthCache()
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgPath := filepath.Join(dir, "nvoi.yaml")
	if err := os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--config", cfgPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--local") {
		t.Fatalf("error = %q, want --local suggestion", err.Error())
	}
}

func TestCloudOnlyCommandsWithLocal(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"login", []string{"login", "--local"}},
		{"whoami", []string{"whoami", "--local"}},
		{"workspaces", []string{"workspaces", "list", "--local"}},
		{"repos", []string{"repos", "list", "--local"}},
		{"provider", []string{"provider", "list", "--local"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := rootCmd()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "not available in local mode") {
				t.Fatalf("error = %q, want 'not available in local mode'", err.Error())
			}
		})
	}
}

// ── Cloud-only auth enforcement ──────────────────────────────────────────────

func TestCloudOnlyCommandsRequireAuth(t *testing.T) {
	cli.ResetAuthCache()
	t.Setenv("HOME", t.TempDir()) // no auth.json

	// These commands should fail at PersistentPreRunE with an auth error,
	// NOT reach their RunE and fail with a different error.
	tests := []struct {
		name string
		args []string
	}{
		{"whoami", []string{"whoami"}},
		{"workspaces", []string{"workspaces", "list"}},
		{"repos", []string{"repos", "list"}},
		{"provider", []string{"provider", "list"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := rootCmd()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "nvoi login") {
				t.Fatalf("error = %q, want auth error mentioning 'nvoi login'", err.Error())
			}
		})
	}
}

func TestLoginDoesNotRequireAuth(t *testing.T) {
	cli.ResetAuthCache()
	t.Setenv("HOME", t.TempDir()) // no auth.json

	cmd := rootCmd()
	cmd.SetArgs([]string{"login"})
	err := cmd.Execute()
	// login will fail (no GitHub token), but NOT with "nvoi login" auth error.
	// It should get past PersistentPreRunE and into its own RunE.
	if err == nil {
		t.Fatal("expected error (no GitHub token)")
	}
	if strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("login should not require auth, got: %q", err.Error())
	}
}

// ── Dispatch: local mode ────────────────────────────────────────────────────

func TestLocalDispatch_Deploy(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	// Config with app+env but no providers.compute — ValidateConfig rejects it.
	// This error is unique to the local path: cloud mode sends YAML to the API
	// without local validation.
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--local", "--config", cfgPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "providers.compute is required") {
		t.Fatalf("error = %q, want validation error from local deploy path", err.Error())
	}
}

func TestLocalDispatch_Teardown(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	// Teardown calls LoadConfig then core.Teardown. With an empty config,
	// Teardown still calls FirewallDelete and NetworkDelete which need a provider.
	// The error will be about provider resolution, not auth.
	cmd := rootCmd()
	cmd.SetArgs([]string{"teardown", "--local", "--config", cfgPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error (no compute provider)")
	}
	if strings.Contains(err.Error(), "nvoi login") {
		t.Fatalf("dispatch went to cloud path: %q", err.Error())
	}
}

// ── Dispatch: cloud mode ────────────────────────────────────────────────────

func writeAuth(t *testing.T, apiBase string) string {
	t.Helper()
	cli.ResetAuthCache()
	t.Cleanup(cli.ResetAuthCache)

	dir := t.TempDir()
	t.Setenv("HOME", dir)

	authDir := filepath.Join(dir, ".config", "nvoi")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	auth := fmt.Sprintf(`{"api_base":%q,"token":"tok","workspace_id":"ws-1","repo_id":"repo-1"}`, apiBase)
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCloudDispatch_Deploy(t *testing.T) {
	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(200) // empty body — StreamRun finishes cleanly
	}))
	defer ts.Close()

	dir := writeAuth(t, ts.URL)
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--config", cfgPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/workspaces/ws-1/repos/repo-1/deploy" {
		t.Fatalf("path = %q, want /workspaces/ws-1/repos/repo-1/deploy", gotPath)
	}
}

func TestCloudDispatch_Describe(t *testing.T) {
	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"services": []any{}, "pods": []any{}})
	}))
	defer ts.Close()

	writeAuth(t, ts.URL)

	cmd := rootCmd()
	cmd.SetArgs([]string{"describe"})
	cmd.Execute() // may error on decode, but API was hit
	if gotMethod != "GET" {
		t.Fatalf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/workspaces/ws-1/repos/repo-1/describe" {
		t.Fatalf("path = %q, want /workspaces/ws-1/repos/repo-1/describe", gotPath)
	}
}

func TestCloudDispatch_Teardown(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer ts.Close()

	dir := writeAuth(t, ts.URL)
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	cmd := rootCmd()
	cmd.SetArgs([]string{"teardown", "--config", cfgPath})
	cmd.Execute()
	if gotPath != "/workspaces/ws-1/repos/repo-1/teardown" {
		t.Fatalf("path = %q, want /workspaces/ws-1/repos/repo-1/teardown", gotPath)
	}
}

func TestCloudDispatch_Resources(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
	}))
	defer ts.Close()

	writeAuth(t, ts.URL)

	cmd := rootCmd()
	cmd.SetArgs([]string{"resources"})
	cmd.Execute()
	if gotPath != "/workspaces/ws-1/repos/repo-1/resources" {
		t.Fatalf("path = %q, want /workspaces/ws-1/repos/repo-1/resources", gotPath)
	}
}

func TestCloudDispatch_Logs(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer ts.Close()

	writeAuth(t, ts.URL)

	cmd := rootCmd()
	cmd.SetArgs([]string{"logs", "web"})
	cmd.Execute()
	if !strings.HasPrefix(gotPath, "/workspaces/ws-1/repos/repo-1/services/web/logs") {
		t.Fatalf("path = %q, want prefix /workspaces/ws-1/repos/repo-1/services/web/logs", gotPath)
	}
}

func TestCloudDispatch_LogsURLEncoding(t *testing.T) {
	var gotRawQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.WriteHeader(200)
	}))
	defer ts.Close()

	writeAuth(t, ts.URL)

	cmd := rootCmd()
	cmd.SetArgs([]string{"logs", "web", "--since", "5m&foo=bar"})
	err := cmd.Execute()
	t.Logf("Execute error: %v", err)
	t.Logf("gotRawQuery: %q", gotRawQuery)
	if strings.Contains(gotRawQuery, "foo=bar") {
		t.Fatalf("since param not escaped — raw query %q contains injected param", gotRawQuery)
	}
	if !strings.Contains(gotRawQuery, "since=5m%26foo%3Dbar") {
		t.Fatalf("raw query = %q, want since=5m%%26foo%%3Dbar (encoded)", gotRawQuery)
	}
}

func TestCloudDispatch_Exec(t *testing.T) {
	var gotPath, gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(200)
	}))
	defer ts.Close()

	writeAuth(t, ts.URL)

	cmd := rootCmd()
	cmd.SetArgs([]string{"exec", "web", "--", "sh"})
	cmd.Execute()
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/workspaces/ws-1/repos/repo-1/services/web/exec" {
		t.Fatalf("path = %q, want /workspaces/ws-1/repos/repo-1/services/web/exec", gotPath)
	}
}

func TestCloudDispatch_SSH(t *testing.T) {
	var gotPath, gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(200)
	}))
	defer ts.Close()

	writeAuth(t, ts.URL)

	cmd := rootCmd()
	cmd.SetArgs([]string{"ssh", "--", "kubectl", "get", "pods"})
	cmd.Execute()
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/workspaces/ws-1/repos/repo-1/ssh" {
		t.Fatalf("path = %q, want /workspaces/ws-1/repos/repo-1/ssh", gotPath)
	}
}

func TestCloudDispatch_CronRun(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer ts.Close()

	writeAuth(t, ts.URL)

	cmd := rootCmd()
	cmd.SetArgs([]string{"cron", "run", "cleanup"})
	cmd.Execute()
	if gotPath != "/workspaces/ws-1/repos/repo-1/run" {
		t.Fatalf("path = %q, want /workspaces/ws-1/repos/repo-1/run", gotPath)
	}
}

func TestCloudDispatch_DatabaseSQL(t *testing.T) {
	var gotPath, gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"output": "1 row"})
	}))
	defer ts.Close()

	writeAuth(t, ts.URL)

	cmd := rootCmd()
	cmd.SetArgs([]string{"db", "sql", "SELECT 1"})
	cmd.Execute()
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/workspaces/ws-1/repos/repo-1/database/sql" {
		t.Fatalf("path = %q, want /workspaces/ws-1/repos/repo-1/database/sql", gotPath)
	}
}

func TestCloudDispatch_DatabaseBackupList(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
	}))
	defer ts.Close()

	writeAuth(t, ts.URL)

	cmd := rootCmd()
	cmd.SetArgs([]string{"db", "backup", "list"})
	cmd.Execute()
	want := "/workspaces/ws-1/repos/repo-1/database/backups?name=main"
	if gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}

// ── Teardown flags forwarded to API ──────────────────────────────────────────

func TestCloudDispatch_TeardownForwardsDeleteFlags(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	dir := writeAuth(t, ts.URL)
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	cmd := rootCmd()
	cmd.SetArgs([]string{"teardown", "--config", cfgPath, "--delete-volumes", "--delete-storage"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["delete_volumes"] != true {
		t.Fatalf("delete_volumes = %v, want true", gotBody["delete_volumes"])
	}
	if gotBody["delete_storage"] != true {
		t.Fatalf("delete_storage = %v, want true", gotBody["delete_storage"])
	}
}

func TestCloudDispatch_TeardownOmitsFlagsWhenFalse(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	dir := writeAuth(t, ts.URL)
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644)

	cmd := rootCmd()
	cmd.SetArgs([]string{"teardown", "--config", cfgPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := gotBody["delete_volumes"]; ok {
		t.Fatal("delete_volumes should not be present when flag is false")
	}
	if _, ok := gotBody["delete_storage"]; ok {
		t.Fatal("delete_storage should not be present when flag is false")
	}
}

// ── StreamRun ───────────────────────────────────────────────────────────────

func TestStreamRun_ProcessesJSONL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintln(w, `{"type":"success","message":"done"}`)
	}))
	defer ts.Close()

	client := cli.NewAPIClient(&cli.AuthConfig{APIBase: ts.URL, Token: "test"})
	err := cli.StreamRun(client, "/test", map[string]any{"key": "val"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStreamRun_ReturnsLastError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintln(w, `{"type":"error","message":"deploy failed"}`)
	}))
	defer ts.Close()

	client := cli.NewAPIClient(&cli.AuthConfig{APIBase: ts.URL, Token: "test"})
	err := cli.StreamRun(client, "/test", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "deploy failed") {
		t.Fatalf("error = %q, want 'deploy failed'", err.Error())
	}
}

func TestStreamRun_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "internal"})
	}))
	defer ts.Close()

	client := cli.NewAPIClient(&cli.AuthConfig{APIBase: ts.URL, Token: "test"})
	err := cli.StreamRun(client, "/test", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "internal") {
		t.Fatalf("error = %q, want 'internal'", err.Error())
	}
}

// ── LoadConfig ──────────────────────────────────────────────────────────────

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	os.WriteFile(cfgPath, []byte("app: myapp\nenv: staging\nproviders:\n  compute: hetzner\nservers:\n  master:\n    type: cax11\n    region: nbg1\n    role: master\n"), 0o644)

	cmd := &cobra.Command{}
	cmd.Flags().String("config", cfgPath, "")

	cfg, err := core.LoadConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.App != "myapp" {
		t.Fatalf("app = %q, want myapp", cfg.App)
	}
	if cfg.Env != "staging" {
		t.Fatalf("env = %q, want staging", cfg.Env)
	}
	if cfg.Providers.Compute != "hetzner" {
		t.Fatalf("compute = %q, want hetzner", cfg.Providers.Compute)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("config", "/nonexistent/nvoi.yaml", "")

	_, err := core.LoadConfig(cmd)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("error = %q, want file-not-found error", err.Error())
	}
}

func TestLoadConfig_DefaultPath(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")

	_, err := core.LoadConfig(cmd)
	// Default "nvoi.yaml" doesn't exist in test dir — error expected.
	if err == nil {
		t.Fatal("expected error")
	}
}
