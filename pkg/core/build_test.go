package core

import (
	"strings"
	"testing"
)

func TestIsLocalSource(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{".", true},
		{"./foo", true},
		{"/abs", true},
		{"org/repo", false},
		{"https://github.com/org/repo", false},
	}
	for _, tt := range tests {
		got := isLocalSource(tt.source)
		if got != tt.want {
			t.Errorf("isLocalSource(%q) = %v, want %v", tt.source, got, tt.want)
		}
	}
}

func TestParseSignedURL(t *testing.T) {
	// Valid signed URL.
	cleanURL, user, token, ok := parseSignedURL("https://user:token@github.com/org/repo")
	if !ok {
		t.Fatal("valid signed URL: got ok=false, want true")
	}
	if cleanURL != "https://github.com/org/repo" {
		t.Errorf("valid signed URL: cleanURL = %q, want %q", cleanURL, "https://github.com/org/repo")
	}
	if user != "user" {
		t.Errorf("valid signed URL: user = %q, want %q", user, "user")
	}
	if token != "token" {
		t.Errorf("valid signed URL: token = %q, want %q", token, "token")
	}

	// No @ sign.
	_, _, _, ok = parseSignedURL("https://github.com/org/repo")
	if ok {
		t.Error("no @ sign: got ok=true, want false")
	}

	// No password (user only).
	_, _, _, ok = parseSignedURL("https://user@github.com/org/repo")
	if ok {
		t.Error("no password: got ok=true, want false")
	}

	// Non-HTTP scheme.
	_, _, _, ok = parseSignedURL("git@github.com:org/repo.git")
	if ok {
		t.Error("non-http scheme: got ok=true, want false")
	}
}

func TestParseBuildTargets_Valid(t *testing.T) {
	targets, err := ParseBuildTargets([]string{"web:./cmd/web", "api:./cmd/api"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(targets))
	}
	if targets[0].Name != "web" || targets[0].Source != "./cmd/web" {
		t.Errorf("target[0] = %+v", targets[0])
	}
}

func TestParseBuildTargets_URLs(t *testing.T) {
	tests := []struct {
		input      string
		wantName   string
		wantSource string
	}{
		{"web:./cmd/web", "web", "./cmd/web"},
		{"web:benbonnet/dummy-rails", "web", "benbonnet/dummy-rails"},
		{"web:https://github.com/org/repo", "web", "https://github.com/org/repo"},
		{"web:git@github.com:org/repo", "web", "git@github.com:org/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			targets, err := ParseBuildTargets([]string{tt.input})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if targets[0].Name != tt.wantName {
				t.Errorf("name = %q, want %q", targets[0].Name, tt.wantName)
			}
			if targets[0].Source != tt.wantSource {
				t.Errorf("source = %q, want %q", targets[0].Source, tt.wantSource)
			}
		})
	}
}

func TestParseBuildTargets_Invalid(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"nocolon", "invalid target"},
		{":nosource", "invalid target"},
		{"noname:", "invalid target"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := ParseBuildTargets([]string{tt.input})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want containing %q", err, tt.want)
			}
		})
	}
}

func TestPrefixWriter(t *testing.T) {
	var buf strings.Builder
	pw := &prefixWriter{prefix: "[web] ", w: &buf}

	pw.Write([]byte("line one\nline two\n"))
	pw.Write([]byte("partial"))
	pw.Write([]byte(" continued\n"))

	got := buf.String()
	want := "[web] line one\n[web] line two\n[web] partial continued\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
