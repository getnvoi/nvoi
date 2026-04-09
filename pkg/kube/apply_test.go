package kube

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestDeleteByNameSuccess(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "delete deployment/", Result: testutil.MockResult{}},
			{Prefix: "delete statefulset/", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
		},
	}

	err := DeleteByName(context.Background(), mock, "myns", "web")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestDeleteByNameSSHError(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "delete deployment/", Result: testutil.MockResult{Err: fmt.Errorf("connection refused")}},
			{Prefix: "delete statefulset/", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
		},
	}

	err := DeleteByName(context.Background(), mock, "myns", "web")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !contains(got, "delete deployment") {
		t.Fatalf("expected error containing %q, got %q", "delete deployment", got)
	}
}

func TestEnsureNamespace(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
		},
	}

	err := EnsureNamespace(context.Background(), mock, "myns")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestFirstPodFound(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte("'web-abc123'")}},
		},
	}

	pod, err := FirstPod(context.Background(), mock, "myns", "web")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if pod != "web-abc123" {
		t.Fatalf("expected pod %q, got %q", "web-abc123", pod)
	}
}

func TestFirstPodNotFound(t *testing.T) {
	sshErr := fmt.Errorf("exit status 1")
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get pods", Result: testutil.MockResult{Err: sshErr}},
		},
	}

	_, err := FirstPod(context.Background(), mock, "myns", "web")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The error should wrap the original SSH error, not mask it.
	if got := err.Error(); !contains(got, "exit status 1") {
		t.Fatalf("expected error to wrap SSH error, got %q", got)
	}
}

func TestGetServicePort(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get service", Result: testutil.MockResult{Output: []byte("'3000'")}},
		},
	}

	port, err := GetServicePort(context.Background(), mock, "myns", "web")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if port != 3000 {
		t.Fatalf("expected port 3000, got %d", port)
	}
}

func TestGetServicePortNotFound(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get service", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
		},
	}

	port, err := GetServicePort(context.Background(), mock, "myns", "web")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if port != 0 {
		t.Fatalf("expected port 0, got %d", port)
	}
}

func TestApply(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "apply --server-side", Result: testutil.MockResult{Output: []byte("deployment/web serverside-applied")}},
		},
	}

	yamlContent := "apiVersion: v1\nkind: Service\nmetadata:\n  name: web\n"
	err := Apply(context.Background(), mock, "myns", yamlContent)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(mock.Uploads) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(mock.Uploads))
	}
	if mock.Uploads[0].Path != utils.KubeManifestPath() {
		t.Fatalf("expected upload path %q, got %q", utils.KubeManifestPath(), mock.Uploads[0].Path)
	}
	if string(mock.Uploads[0].Content) != yamlContent {
		t.Fatalf("expected upload content %q, got %q", yamlContent, string(mock.Uploads[0].Content))
	}
}

func TestApply_Error(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "apply --server-side", Result: testutil.MockResult{Output: []byte("Error: field is immutable"), Err: fmt.Errorf("exit status 1")}},
		},
	}

	err := Apply(context.Background(), mock, "myns", "apiVersion: v1\n")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "kubectl apply") {
		t.Errorf("error should mention kubectl apply, got: %q", err)
	}
}

func TestListSecretKeys(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret", Result: testutil.MockResult{Output: []byte(`'{"DB_PASS":"base64","RAILS_KEY":"base64"}'`)}},
		},
	}

	keys, err := ListSecretKeys(context.Background(), mock, "myns", "nvoi-secrets")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	sort.Strings(keys)
	expected := []string{"DB_PASS", "RAILS_KEY"}
	sort.Strings(expected)

	if len(keys) != len(expected) {
		t.Fatalf("expected %d keys, got %d", len(expected), len(keys))
	}
	for i := range expected {
		if keys[i] != expected[i] {
			t.Fatalf("expected key %q at index %d, got %q", expected[i], i, keys[i])
		}
	}
}

func TestListSecretKeysEmpty(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret", Result: testutil.MockResult{Output: []byte("'{}'")}},
		},
	}

	keys, err := ListSecretKeys(context.Background(), mock, "myns", "nvoi-secrets")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if keys != nil {
		t.Fatalf("expected nil keys, got %v", keys)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty string", input: "", want: "''"},
		{name: "simple string", input: "hello", want: "'hello'"},
		{name: "string with single quotes", input: "it's", want: "'it'\\''s'"},
		{name: "string with spaces", input: "hello world", want: "'hello world'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Fatalf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEscapeJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain string", input: "hello", want: "hello"},
		{name: "string with backslash", input: `back\slash`, want: `back\\slash`},
		{name: "string with double quote", input: `say "hi"`, want: `say \"hi\"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeJSON(tt.input)
			if got != tt.want {
				t.Fatalf("escapeJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPodSelector(t *testing.T) {
	got := PodSelector("web")
	want := utils.LabelAppName + "=web"
	if got != want {
		t.Fatalf("PodSelector(%q) = %q, want %q", "web", got, want)
	}
}

// contains is a helper to check substring presence without importing strings in tests.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstr(s, substr)
}

func searchSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
