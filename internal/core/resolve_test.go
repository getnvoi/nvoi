package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo", home + "/foo"},
		{"~/.ssh/id_rsa", home + "/.ssh/id_rsa"},
		{"~/", home + "/"},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"", ""},
		{"~notauser/foo", "~notauser/foo"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := expandHome(tt.input)
			if got != tt.want {
				t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveSSHKey_TildeExpansion(t *testing.T) {
	// Write a temp key to a temp dir, set SSH_KEY_PATH=~/relative
	dir := t.TempDir()
	keyContent := []byte("fake-ssh-key-content")
	keyFile := filepath.Join(dir, "test_key")
	if err := os.WriteFile(keyFile, keyContent, 0600); err != nil {
		t.Fatal(err)
	}

	// Use tilde path by setting HOME to the temp dir's parent
	// and SSH_KEY_PATH to ~/basename
	home := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	t.Setenv("SSH_KEY_PATH", "~/test_key")

	key, err := resolveSSHKey()
	if err != nil {
		t.Fatalf("resolveSSHKey with tilde path: %v", err)
	}
	if string(key) != string(keyContent) {
		t.Errorf("got %q, want %q", key, keyContent)
	}

	// Restore and verify absolute path still works
	t.Setenv("HOME", home)
	t.Setenv("SSH_KEY_PATH", keyFile)
	key, err = resolveSSHKey()
	if err != nil {
		t.Fatalf("resolveSSHKey with absolute path: %v", err)
	}
	if string(key) != string(keyContent) {
		t.Errorf("got %q, want %q", key, keyContent)
	}
}

func TestResolveSSHKey_FallbackDiscovery(t *testing.T) {
	dir := t.TempDir()
	sshDir := filepath.Join(dir, ".ssh")
	os.MkdirAll(sshDir, 0700)

	keyContent := []byte("ed25519-key-data")
	os.WriteFile(filepath.Join(sshDir, "id_ed25519"), keyContent, 0600)

	t.Setenv("HOME", dir)
	t.Setenv("SSH_KEY_PATH", "")

	key, err := resolveSSHKey()
	if err != nil {
		t.Fatalf("resolveSSHKey fallback: %v", err)
	}
	if string(key) != string(keyContent) {
		t.Errorf("got %q, want %q", key, keyContent)
	}
}

func TestResolveSSHKey_MissingKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("SSH_KEY_PATH", "")

	_, err := resolveSSHKey()
	if err == nil {
		t.Fatal("expected error when no SSH key exists")
	}
}
