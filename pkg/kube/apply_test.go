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

func TestApplyGlobal(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "apply --server-side", Result: testutil.MockResult{Output: []byte("helmchartconfig/traefik serverside-applied")}},
		},
	}

	yamlContent := "apiVersion: helm.cattle.io/v1\nkind: HelmChartConfig\nmetadata:\n  name: traefik\n"
	err := ApplyGlobal(context.Background(), mock, yamlContent)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Must use SFTP upload, same as Apply
	if len(mock.Uploads) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(mock.Uploads))
	}
	if mock.Uploads[0].Path != utils.KubeManifestPath() {
		t.Fatalf("expected upload path %q, got %q", utils.KubeManifestPath(), mock.Uploads[0].Path)
	}
	if string(mock.Uploads[0].Content) != yamlContent {
		t.Fatalf("expected upload content %q, got %q", yamlContent, string(mock.Uploads[0].Content))
	}

	// Must use kubectlGlobal (no -n namespace flag)
	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}
	cmd := mock.Calls[0]
	if contains(cmd, " -n ") {
		t.Errorf("ApplyGlobal should not pass a namespace flag, got: %s", cmd)
	}
	if !contains(cmd, "KUBECONFIG=") {
		t.Errorf("ApplyGlobal should use KUBECONFIG env, got: %s", cmd)
	}
}

func TestApplyGlobal_Error(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "apply --server-side", Result: testutil.MockResult{Output: []byte("Error: unknown type"), Err: fmt.Errorf("exit status 1")}},
		},
	}

	err := ApplyGlobal(context.Background(), mock, "invalid yaml")
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

func TestLabelNode(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "label node", Result: testutil.MockResult{}},
		},
	}

	err := LabelNode(context.Background(), mock, "master", "master")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}
	cmd := mock.Calls[0]
	if !contains(cmd, "KUBECONFIG=") {
		t.Errorf("LabelNode should use KUBECONFIG env, got: %s", cmd)
	}
	// Must NOT have -n namespace flag (cluster-scoped)
	if contains(cmd, " -n ") {
		t.Errorf("LabelNode should not pass a namespace flag, got: %s", cmd)
	}
	if !contains(cmd, "--overwrite") {
		t.Errorf("LabelNode should use --overwrite, got: %s", cmd)
	}
}

// ── UpsertSecretKey ─────────────────────────────────────────────────────────

func TestUpsertSecretKey_CreateNew(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// get secret → not found (triggers create path)
			{Prefix: "get secret", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			// create secret → success
			{Prefix: "create secret generic", Result: testutil.MockResult{}},
		},
	}

	err := UpsertSecretKey(context.Background(), mock, "ns", "nvoi-secrets", "DB_PASS", "s3cret")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Should NOT upload a patch file (create path uses --from-literal)
	if len(mock.Uploads) != 0 {
		t.Errorf("create path should not upload patch file, got %d uploads", len(mock.Uploads))
	}
}

func TestUpsertSecretKey_PatchExisting(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// get secret → exists
			{Prefix: "get secret", Result: testutil.MockResult{Output: []byte("nvoi-secrets")}},
			// patch secret → success
			{Prefix: "patch secret", Result: testutil.MockResult{}},
		},
	}

	err := UpsertSecretKey(context.Background(), mock, "ns", "nvoi-secrets", "DB_PASS", "s3cret")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Must upload patch file to avoid shell injection
	if len(mock.Uploads) != 1 {
		t.Fatalf("patch path must upload JSON patch file, got %d uploads", len(mock.Uploads))
	}
	if mock.Uploads[0].Mode != 0o600 {
		t.Errorf("patch file mode = %o, want 0600", mock.Uploads[0].Mode)
	}
	// Patch content should contain the key+value
	patch := string(mock.Uploads[0].Content)
	if !contains(patch, "DB_PASS") || !contains(patch, "s3cret") {
		t.Errorf("patch should contain key and value, got: %s", patch)
	}
}

// ── DeleteSecretKey ─────────────────────────────────────────────────────────

func TestDeleteSecretKey_SecretDoesNotExist(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
		},
	}

	err := DeleteSecretKey(context.Background(), mock, "ns", "nvoi-secrets", "DB_PASS")
	if err != nil {
		t.Fatalf("should be idempotent when secret doesn't exist, got %v", err)
	}
}

func TestDeleteSecretKey_KeyDoesNotExist(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// secret exists
			{Prefix: "get secret nvoi-secrets 2", Result: testutil.MockResult{Output: []byte("nvoi-secrets")}},
			// list keys → has OTHER_KEY but not DB_PASS
			{Prefix: "get secret nvoi-secrets -o jsonpath", Result: testutil.MockResult{Output: []byte(`'{"OTHER_KEY":"base64"}'`)}},
		},
	}

	err := DeleteSecretKey(context.Background(), mock, "ns", "nvoi-secrets", "DB_PASS")
	if err != nil {
		t.Fatalf("should be idempotent when key doesn't exist, got %v", err)
	}
}

func TestDeleteSecretKey_RemovesKey(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// secret exists
			{Prefix: "get secret nvoi-secrets 2", Result: testutil.MockResult{Output: []byte("nvoi-secrets")}},
			// list keys → has DB_PASS
			{Prefix: "get secret nvoi-secrets -o jsonpath", Result: testutil.MockResult{Output: []byte(`'{"DB_PASS":"base64"}'`)}},
			// patch to remove
			{Prefix: "patch secret", Result: testutil.MockResult{}},
		},
	}

	err := DeleteSecretKey(context.Background(), mock, "ns", "nvoi-secrets", "DB_PASS")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// ── DrainAndRemoveNode ──────────────────────────────────────────────────────

func TestDrainAndRemoveNode_NodeDoesNotExist(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// get node → not found
			{Prefix: "get node", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
		},
	}

	err := DrainAndRemoveNode(context.Background(), mock, "worker-1")
	if err != nil {
		t.Fatalf("should be idempotent for absent node, got %v", err)
	}
}

func TestDrainAndRemoveNode_DrainSucceeds(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// get node → exists
			{Prefix: "get node worker-1 --no-headers", Result: testutil.MockResult{Output: []byte("worker-1   Ready   <none>")}},
			// drain → success
			{Prefix: "drain worker-1", Result: testutil.MockResult{}},
			// delete node
			{Prefix: "delete node", Result: testutil.MockResult{}},
		},
	}

	err := DrainAndRemoveNode(context.Background(), mock, "worker-1")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestDrainAndRemoveNode_DrainFailsNodeReady_ReturnsError(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// get node → exists
			{Prefix: "get node worker-1 --no-headers", Result: testutil.MockResult{Output: []byte("worker-1   Ready   <none>")}},
			// drain → fails
			{Prefix: "drain worker-1", Result: testutil.MockResult{Err: fmt.Errorf("cannot evict pod")}},
			// check status → Ready (node is alive)
			{Prefix: "jsonpath", Result: testutil.MockResult{Output: []byte("'True'")}},
		},
	}

	err := DrainAndRemoveNode(context.Background(), mock, "worker-1")
	if err == nil {
		t.Fatal("should return error when drain fails on Ready node")
	}
	if !contains(err.Error(), "drain node") {
		t.Errorf("error should mention drain, got: %s", err)
	}
}

func TestDrainAndRemoveNode_DrainFailsNodeNotReady_ForceRemoves(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// get node → exists
			{Prefix: "get node worker-1 --no-headers", Result: testutil.MockResult{Output: []byte("worker-1   NotReady   <none>")}},
			// drain → fails
			{Prefix: "drain worker-1", Result: testutil.MockResult{Err: fmt.Errorf("node unreachable")}},
			// check status → NotReady (dead node)
			{Prefix: "jsonpath", Result: testutil.MockResult{Output: []byte("'False'")}},
			// delete node → force-remove succeeds
			{Prefix: "delete node", Result: testutil.MockResult{}},
		},
	}

	err := DrainAndRemoveNode(context.Background(), mock, "worker-1")
	if err != nil {
		t.Fatalf("should force-remove NotReady node, got %v", err)
	}
}

// ── GetSecretValue ──────────────────────────────────────────────────────────

func TestGetSecretValue_Success(t *testing.T) {
	// base64("hello") = "aGVsbG8="
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret", Result: testutil.MockResult{Output: []byte("'aGVsbG8='")}},
		},
	}

	val, err := GetSecretValue(context.Background(), mock, "ns", "nvoi-secrets", "MY_KEY")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if val != "hello" {
		t.Errorf("value = %q, want %q", val, "hello")
	}
}

func TestGetSecretValue_NotFound(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
		},
	}

	_, err := GetSecretValue(context.Background(), mock, "ns", "nvoi-secrets", "MISSING")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

// ── DeleteCronByName ────────────────────────────────────────────────────────

func TestDeleteCronByName(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "delete cronjob/", Result: testutil.MockResult{}},
		},
	}

	err := DeleteCronByName(context.Background(), mock, "ns", "cleanup")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}
	if !contains(mock.Calls[0], "KUBECONFIG=") {
		t.Errorf("should use KUBECONFIG, got: %s", mock.Calls[0])
	}
}

// ── DeleteIngress ───────────────────────────────────────────────────────────

func TestDeleteIngress(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "delete ingress", Result: testutil.MockResult{}},
		},
	}

	err := DeleteIngress(context.Background(), mock, "ns", "web")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}
	cmd := mock.Calls[0]
	if !contains(cmd, "ingress-web") {
		t.Errorf("should delete ingress-web, got: %s", cmd)
	}
	if !contains(cmd, "--ignore-not-found") {
		t.Errorf("should use --ignore-not-found, got: %s", cmd)
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
