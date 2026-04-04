package core

import "testing"

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
