package reconcile

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
)

// fetchPullSecret returns the managed pull secret from the app namespace,
// or nil if absent. Tests use this to assert apply / delete behavior
// without coupling to kc internals.
func fetchPullSecret(t *testing.T, dc *config.DeployContext) *corev1.Secret {
	t.Helper()
	sec, err := kfFor(dc).Typed.CoreV1().Secrets(testNS).Get(
		context.Background(), kube.PullSecretName, metav1.GetOptions{},
	)
	if err != nil {
		return nil
	}
	return sec
}

func TestRegistries_AppliesDockerConfigJSONSecret(t *testing.T) {
	dc := testDCWithCreds(convergeMock(),
		"DOCKER_USERNAME", "alice",
		"DOCKER_PASSWORD", "hub-token",
		"GITHUB_USERNAME", "alice",
		"GITHUB_TOKEN", "ghp_xyz",
	)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Registry: map[string]config.RegistryDef{
			"docker.io": {Username: "$DOCKER_USERNAME", Password: "$DOCKER_PASSWORD"},
			"ghcr.io":   {Username: "$GITHUB_USERNAME", Password: "$GITHUB_TOKEN"},
		},
	}

	if err := Registries(context.Background(), dc, cfg); err != nil {
		t.Fatalf("Registries: %v", err)
	}

	sec := fetchPullSecret(t, dc)
	if sec == nil {
		t.Fatal("pull secret not created")
	}
	if sec.Type != corev1.SecretTypeDockerConfigJson {
		t.Errorf("type = %q, want %q", sec.Type, corev1.SecretTypeDockerConfigJson)
	}
	data, ok := sec.Data[corev1.DockerConfigJsonKey]
	if !ok {
		t.Fatalf("Data[%q] missing", corev1.DockerConfigJsonKey)
	}

	// Decode the rendered JSON and verify both hosts resolved to their
	// actual credentials (not leaving $VAR-literal strings in the output).
	var parsed struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse: %v\nraw: %s", err, data)
	}
	if got := parsed.Auths["docker.io"].Username; got != "alice" {
		t.Errorf("docker.io username = %q, want alice — did $VAR resolution fire?", got)
	}
	if got := parsed.Auths["docker.io"].Password; got != "hub-token" {
		t.Errorf("docker.io password = %q, want hub-token", got)
	}
	if got := parsed.Auths["ghcr.io"].Password; got != "ghp_xyz" {
		t.Errorf("ghcr.io password = %q, want ghp_xyz", got)
	}
	// REGRESSION GUARD: no literal $VAR strings should leak into the Secret.
	if strings.Contains(string(data), "$DOCKER_PASSWORD") || strings.Contains(string(data), "$GITHUB_TOKEN") {
		t.Errorf("unresolved $VAR leaked into secret: %s", data)
	}
}

func TestRegistries_LiteralCredsPassThrough(t *testing.T) {
	// No $VAR syntax — literal username/password. Should apply as-is.
	dc := testDCWithCreds(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Registry: map[string]config.RegistryDef{
			"registry.example.com": {Username: "ci", Password: "plaintext"},
		},
	}
	if err := Registries(context.Background(), dc, cfg); err != nil {
		t.Fatalf("Registries: %v", err)
	}
	sec := fetchPullSecret(t, dc)
	if sec == nil {
		t.Fatal("pull secret not created")
	}
	if !strings.Contains(string(sec.Data[corev1.DockerConfigJsonKey]), "plaintext") {
		t.Errorf("literal password should pass through untouched: %s", sec.Data[corev1.DockerConfigJsonKey])
	}
}

func TestRegistries_MissingEnvVar_HardError(t *testing.T) {
	// $DOCKER_PASSWORD is referenced but never seeded into CredentialSource.
	// Must surface a readable error, not silently fall through.
	dc := testDCWithCreds(convergeMock(), "DOCKER_USERNAME", "alice")
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Registry: map[string]config.RegistryDef{
			"docker.io": {Username: "$DOCKER_USERNAME", Password: "$DOCKER_PASSWORD"},
		},
	}
	err := Registries(context.Background(), dc, cfg)
	if err == nil {
		t.Fatal("expected error for unresolved $VAR, got nil")
	}
	if !strings.Contains(err.Error(), "DOCKER_PASSWORD") {
		t.Errorf("error should name the missing var, got: %v", err)
	}
	if !strings.Contains(err.Error(), "docker.io") {
		t.Errorf("error should name the registry, got: %v", err)
	}
	// No Secret must have been created on failure.
	if sec := fetchPullSecret(t, dc); sec != nil {
		t.Errorf("no Secret expected on failed resolution, got: %+v", sec)
	}
}

func TestRegistries_EmptyBlock_DeletesOrphanSecret(t *testing.T) {
	// First deploy: user had a private registry, Secret landed in the
	// cluster. Second deploy: user removed `registry:` entirely. The prior
	// Secret must be scrubbed — otherwise stale creds linger forever.
	dc := testDCWithCreds(convergeMock(), "DOCKER_PASSWORD", "token")

	// Simulate a prior-deploy leftover.
	kf := kfFor(dc)
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: kube.PullSecretName, Namespace: testNS},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"docker.io":{}}}`)},
	}
	if _, err := kf.Typed.CoreV1().Secrets(testNS).Create(context.Background(), existing, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		// Registry intentionally omitted.
	}
	if err := Registries(context.Background(), dc, cfg); err != nil {
		t.Fatalf("Registries: %v", err)
	}
	if sec := fetchPullSecret(t, dc); sec != nil {
		t.Errorf("orphan Secret must be deleted when registry block is absent, got: %+v", sec)
	}
}

func TestRegistries_EmptyBlock_NoPriorSecret_NoOp(t *testing.T) {
	// No registry block, no prior Secret: Registries must be a no-op, not
	// error. Kube DeleteSecret is already NotFound-safe.
	dc := testDCWithCreds(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	if err := Registries(context.Background(), dc, cfg); err != nil {
		t.Fatalf("no-op must not error, got: %v", err)
	}
}
