package kube

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// ─── BuildDockerConfigJSON ───────────────────────────────────────────────────

// INVARIANT: output is deterministic byte-for-byte when input is identical —
// the host map iterates in sorted order so Apply's update decision never
// flaps, and reload tests can assert on exact bytes.
func TestBuildDockerConfigJSON_Deterministic(t *testing.T) {
	in := map[string]RegistryAuth{
		"ghcr.io":   {Username: "alice", Password: "token1"},
		"docker.io": {Username: "bob", Password: "token2"},
	}
	first, err := BuildDockerConfigJSON(in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := BuildDockerConfigJSON(in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("non-deterministic output:\nfirst:  %s\nsecond: %s", first, second)
	}
	// Sorted order: docker.io < ghcr.io (bytes, with "." < "g"); the
	// rendered bytes should have docker.io's key appear before ghcr.io's.
	if strings.Index(string(first), `"docker.io"`) > strings.Index(string(first), `"ghcr.io"`) {
		t.Errorf("hosts not sorted: %s", first)
	}
}

// INVARIANT: each host entry carries username, password, AND auth
// (base64(user:pass)) — older kubelet versions use `auth`, newer ones use
// the explicit fields. Populating both keeps the Secret compatible across
// k3s versions.
func TestBuildDockerConfigJSON_PopulatesBothAuthForms(t *testing.T) {
	in := map[string]RegistryAuth{
		"ghcr.io": {Username: "alice", Password: "token1"},
	}
	raw, err := BuildDockerConfigJSON(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var cfg dockerConfigJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, raw)
	}
	got, ok := cfg.Auths["ghcr.io"]
	if !ok {
		t.Fatalf("missing ghcr.io entry: %+v", cfg)
	}
	if got.Username != "alice" || got.Password != "token1" {
		t.Errorf("fields wrong: %+v", got)
	}
	wantAuth := base64.StdEncoding.EncodeToString([]byte("alice:token1"))
	if got.Auth != wantAuth {
		t.Errorf("auth = %q, want %q", got.Auth, wantAuth)
	}
}

// INVARIANT: empty creds map produces valid empty-auths JSON (no error).
// Used during orphan-cleanup paths where we might build-and-apply before
// deciding whether to delete.
func TestBuildDockerConfigJSON_EmptyMap(t *testing.T) {
	raw, err := BuildDockerConfigJSON(nil)
	if err != nil {
		t.Fatalf("empty map must be valid, got: %v", err)
	}
	var cfg dockerConfigJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Auths) != 0 {
		t.Errorf("auths must be empty, got: %+v", cfg.Auths)
	}
}

// INVARIANT: empty username/password post-resolution is a hard error. The
// operator forgot to set an env var or the value resolved to empty string
// — both are deploy-time bugs we must surface, never silently accept.
func TestBuildDockerConfigJSON_RejectsEmptyCreds(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   RegistryAuth
	}{
		{"empty user", RegistryAuth{Username: "", Password: "p"}},
		{"empty pass", RegistryAuth{Username: "u", Password: ""}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildDockerConfigJSON(map[string]RegistryAuth{"ghcr.io": tt.in})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// ─── BuildPullSecret ─────────────────────────────────────────────────────────

// INVARIANT: the Secret is of type kubernetes.io/dockerconfigjson and
// holds the rendered JSON under the .dockerconfigjson key. Any other
// Type/key combo means kubelet silently ignores imagePullSecrets.
func TestBuildPullSecret_KubeletReadableShape(t *testing.T) {
	in := map[string]RegistryAuth{
		"ghcr.io": {Username: "alice", Password: "token1"},
	}
	sec, err := BuildPullSecret("nvoi-myapp-prod", in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if sec.Type != corev1.SecretTypeDockerConfigJson {
		t.Errorf("type = %q, want %q — kubelet ignores other types", sec.Type, corev1.SecretTypeDockerConfigJson)
	}
	data, ok := sec.Data[corev1.DockerConfigJsonKey]
	if !ok {
		t.Fatalf("Data[%q] missing — kubelet looks for exactly this key", corev1.DockerConfigJsonKey)
	}
	if !strings.Contains(string(data), "ghcr.io") || !strings.Contains(string(data), "alice") {
		t.Errorf("data doesn't include resolved creds: %s", data)
	}
	if sec.Name != PullSecretName {
		t.Errorf("name = %q, want %q", sec.Name, PullSecretName)
	}
	if sec.Namespace != "nvoi-myapp-prod" {
		t.Errorf("namespace = %q", sec.Namespace)
	}
}
