package local

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestResolveBuild_Registered(t *testing.T) {
	// local provider has no required credentials
	p, err := provider.ResolveBuild("local", map[string]string{})
	if err != nil {
		t.Fatalf("ResolveBuild: %v", err)
	}
	if p == nil {
		t.Fatal("ResolveBuild returned nil")
	}
}

func TestResolveDockerfile_Default(t *testing.T) {
	// Create a temp dir with a Dockerfile
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine"), 0644); err != nil {
		t.Fatal(err)
	}

	df, ctx, err := resolveDockerfile(dir, "")
	if err != nil {
		t.Fatalf("resolveDockerfile: %v", err)
	}
	if filepath.Base(df) != "Dockerfile" {
		t.Errorf("dockerfile = %q, want Dockerfile", df)
	}
	if ctx == "" {
		t.Error("context should not be empty")
	}
}

func TestResolveDockerfile_CustomPath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "docker")
	os.MkdirAll(sub, 0755)
	if err := os.WriteFile(filepath.Join(sub, "Dockerfile.prod"), []byte("FROM alpine"), 0644); err != nil {
		t.Fatal(err)
	}

	df, _, err := resolveDockerfile(dir, "docker/Dockerfile.prod")
	if err != nil {
		t.Fatalf("resolveDockerfile: %v", err)
	}
	if filepath.Base(df) != "Dockerfile.prod" {
		t.Errorf("dockerfile = %q, want Dockerfile.prod", df)
	}
}

func TestResolveDockerfile_Missing(t *testing.T) {
	dir := t.TempDir()
	_, _, err := resolveDockerfile(dir, "")
	if err == nil {
		t.Fatal("expected error for missing Dockerfile")
	}
}

func TestFindProjectRoot_WithGitDir(t *testing.T) {
	// Create dir/sub with .git at dir
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	sub := filepath.Join(dir, "cmd", "web")
	os.MkdirAll(sub, 0755)

	got := findProjectRoot(sub)
	if got != dir {
		t.Errorf("findProjectRoot = %q, want %q", got, dir)
	}
}

func TestFindProjectRoot_WithGoMod(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	sub := filepath.Join(dir, "pkg", "foo")
	os.MkdirAll(sub, 0755)

	got := findProjectRoot(sub)
	if got != dir {
		t.Errorf("findProjectRoot = %q, want %q", got, dir)
	}
}

func TestFindProjectRoot_NoMarker(t *testing.T) {
	dir := t.TempDir()
	// No .git or go.mod — should return the input dir itself
	got := findProjectRoot(dir)
	if got != dir {
		t.Errorf("findProjectRoot = %q, want %q (input itself)", got, dir)
	}
}

func TestFixPermissions(t *testing.T) {
	dir := t.TempDir()

	// Create a regular file and a bin/ file
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0600)
	os.MkdirAll(filepath.Join(dir, "bin"), 0755)
	os.WriteFile(filepath.Join(dir, "bin", "start"), []byte("#!/bin/sh"), 0600)

	if err := fixPermissions(dir); err != nil {
		t.Fatalf("fixPermissions: %v", err)
	}

	// main.go should be 644
	info, _ := os.Stat(filepath.Join(dir, "main.go"))
	if info.Mode().Perm() != 0644 {
		t.Errorf("main.go perm = %o, want 644", info.Mode().Perm())
	}

	// bin/start should be 755
	info, _ = os.Stat(filepath.Join(dir, "bin", "start"))
	if info.Mode().Perm() != 0755 {
		t.Errorf("bin/start perm = %o, want 755", info.Mode().Perm())
	}
}
