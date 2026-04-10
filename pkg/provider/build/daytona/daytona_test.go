package daytona

import (
	"bytes"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestResolveBuild_Registered(t *testing.T) {
	creds := map[string]string{"api_key": "test-key"}
	p, err := provider.ResolveBuild("daytona", creds)
	if err != nil {
		t.Fatalf("ResolveBuild: %v", err)
	}
	if p == nil {
		t.Fatal("ResolveBuild returned nil")
	}
}

func TestResolveBuild_MissingAPIKey(t *testing.T) {
	_, err := provider.ResolveBuild("daytona", map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing api_key")
	}
	if !contains(err.Error(), "api_key") {
		t.Errorf("error %q should mention api_key", err.Error())
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"org/repo", "https://github.com/org/repo.git"},
		{"https://github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"git@github.com:org/repo.git", "git@github.com:org/repo.git"},
		{"http://github.com/org/repo", "http://github.com/org/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeRepoURL(tt.input)
			if got != tt.want {
				t.Errorf("normalizeRepoURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"web", "web"},
		{"my/service", "my-service"},
		{"my:tag", "my-tag"},
		{"a/b:c", "a-b-c"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitize(tt.input)
			if got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitize_Truncates(t *testing.T) {
	long := ""
	for i := 0; i < 60; i++ {
		long += "a"
	}
	got := sanitize(long)
	if len(got) != 50 {
		t.Errorf("sanitize should truncate to 50 chars, got %d", len(got))
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"it's", "'it'\\''s'"},
		{"simple", "'simple'"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCommandError(t *testing.T) {
	err := commandError("build", "some output", 1, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "build") || !contains(err.Error(), "some output") {
		t.Errorf("error %q should mention step and output", err.Error())
	}
}

func TestCommandError_NoOutput(t *testing.T) {
	err := commandError("push", "", 127, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "push") || !contains(err.Error(), "127") {
		t.Errorf("error %q should mention step and exit code", err.Error())
	}
}

func TestLogChunk(t *testing.T) {
	var buf bytes.Buffer
	var lines []string
	emit := func(s string) { lines = append(lines, s) }

	logChunk(&buf, "hello\nworld\n", emit)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "hello" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "hello")
	}
	if lines[1] != "world" {
		t.Errorf("lines[1] = %q, want %q", lines[1], "world")
	}
}

func TestLogChunk_Partial(t *testing.T) {
	var buf bytes.Buffer
	var lines []string
	emit := func(s string) { lines = append(lines, s) }

	logChunk(&buf, "partial", emit)
	if len(lines) != 0 {
		t.Fatalf("partial line should not emit, got %v", lines)
	}
	if buf.String() != "partial" {
		t.Errorf("buf = %q, want %q", buf.String(), "partial")
	}

	logChunk(&buf, " line\n", emit)
	if len(lines) != 1 || lines[0] != "partial line" {
		t.Errorf("expected [partial line], got %v", lines)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
