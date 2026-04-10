package kube

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestGenerateCaddyfileSingleRoute(t *testing.T) {
	routes := []IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"example.com"}},
	}
	out := generateCaddyfile(routes, "nvoi-myapp-production")

	if !strings.Contains(out, "example.com") {
		t.Errorf("expected Caddyfile to contain domain, got:\n%s", out)
	}
	want := "reverse_proxy web.nvoi-myapp-production.svc.cluster.local:3000"
	if !strings.Contains(out, want) {
		t.Errorf("expected Caddyfile to contain %q, got:\n%s", want, out)
	}
}

func TestGenerateCaddyfileMultipleRoutesSorted(t *testing.T) {
	routes := []IngressRoute{
		{Service: "api", Port: 8080, Domains: []string{"z-api.example.com"}},
		{Service: "web", Port: 3000, Domains: []string{"a-web.example.com"}},
	}
	out := generateCaddyfile(routes, "ns")

	idxA := strings.Index(out, "a-web.example.com")
	idxZ := strings.Index(out, "z-api.example.com")
	if idxA < 0 || idxZ < 0 {
		t.Fatalf("expected both domains in output, got:\n%s", out)
	}
	if idxA >= idxZ {
		t.Errorf("expected a-web before z-api (sorted), got:\n%s", out)
	}
}

func TestGenerateCaddyfileMultiDomainRoute(t *testing.T) {
	routes := []IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"example.com", "www.example.com"}},
	}
	out := generateCaddyfile(routes, "ns")

	if !strings.Contains(out, "example.com, www.example.com") {
		t.Errorf("expected domains separated by \", \", got:\n%s", out)
	}
}

func TestParseCaddyfile(t *testing.T) {
	caddyfile := `app.example.com {
	reverse_proxy web.nvoi-myapp-production.svc.cluster.local:3000
}
`
	routes := parseCaddyfile(caddyfile)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	r := routes[0]
	if r.Service != "web" {
		t.Errorf("expected service %q, got %q", "web", r.Service)
	}
	if r.Port != 3000 {
		t.Errorf("expected port 3000, got %d", r.Port)
	}
	if len(r.Domains) != 1 || r.Domains[0] != "app.example.com" {
		t.Errorf("expected domains [app.example.com], got %v", r.Domains)
	}
}

func TestRoundTrip(t *testing.T) {
	original := []IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"example.com", "www.example.com"}},
		{Service: "api", Port: 8080, Domains: []string{"api.example.com"}},
	}
	ns := "nvoi-myapp-production"
	caddyfile := generateCaddyfile(original, ns)
	parsed := parseCaddyfile(caddyfile)

	if len(parsed) != len(original) {
		t.Fatalf("expected %d routes, got %d", len(original), len(parsed))
	}

	// generateCaddyfile sorts by first domain, so expected order is api then web
	expected := []IngressRoute{
		{Service: "api", Port: 8080, Domains: []string{"api.example.com"}},
		{Service: "web", Port: 3000, Domains: []string{"example.com", "www.example.com"}},
	}
	for i, want := range expected {
		got := parsed[i]
		if got.Service != want.Service {
			t.Errorf("route[%d] service: got %q, want %q", i, got.Service, want.Service)
		}
		if got.Port != want.Port {
			t.Errorf("route[%d] port: got %d, want %d", i, got.Port, want.Port)
		}
		if len(got.Domains) != len(want.Domains) {
			t.Errorf("route[%d] domains: got %v, want %v", i, got.Domains, want.Domains)
			continue
		}
		for j := range want.Domains {
			if got.Domains[j] != want.Domains[j] {
				t.Errorf("route[%d] domain[%d]: got %q, want %q", i, j, got.Domains[j], want.Domains[j])
			}
		}
	}
}

func TestParseCaddyfileEmpty(t *testing.T) {
	routes := parseCaddyfile("")
	if routes != nil {
		t.Errorf("expected nil for empty input, got %v", routes)
	}
}

// ── ApplyCaddyConfig tests ──────────────────────────────────────────────────

func applyCaddySSH(deploymentExists bool, expectedHash string) *testutil.MockSSH {
	getDeployResult := testutil.MockResult{Err: fmt.Errorf("not found")}
	if deploymentExists {
		getDeployResult = testutil.MockResult{Output: []byte("ok")}
	}
	return &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "apply --server-side", Result: testutil.MockResult{}},
			{Prefix: "get deployment", Result: getDeployResult},
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte("'caddy-abc123'")}},
			{Prefix: "exec caddy-abc123 -- sha256sum", Result: testutil.MockResult{Output: []byte(expectedHash + "  /etc/caddy/Caddyfile")}},
			{Prefix: "exec caddy-abc123 -- caddy reload", Result: testutil.MockResult{}},
		},
	}
}

func TestApplyCaddyConfig_FirstDeploy(t *testing.T) {
	origDelay := CaddyConfigTimeout
	CaddyConfigTimeout = 10 * time.Millisecond
	defer func() { CaddyConfigTimeout = origDelay }()

	names, _ := utils.NewNames("myapp", "prod")
	routes := []IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"example.com"}},
	}

	ssh := applyCaddySSH(false, "") // deployment doesn't exist
	err := ApplyCaddyConfig(context.Background(), ssh, names.KubeNamespace(), routes, names)
	if err != nil {
		t.Fatalf("first deploy should succeed, got: %v", err)
	}

	// Should have uploaded ConfigMap and Deployment manifests
	if len(ssh.Uploads) < 2 {
		t.Errorf("expected at least 2 uploads (configmap + deployment), got %d", len(ssh.Uploads))
	}
}

func TestApplyCaddyConfig_HotReload(t *testing.T) {
	origDelay := CaddyConfigTimeout
	CaddyConfigTimeout = 100 * time.Millisecond
	defer func() { CaddyConfigTimeout = origDelay }()

	names, _ := utils.NewNames("myapp", "prod")
	routes := []IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"example.com"}},
	}

	// Compute the expected hash
	caddyfile := generateCaddyfile(routes, names.KubeNamespace())
	expectedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(caddyfile)))

	ssh := applyCaddySSH(true, expectedHash) // deployment exists
	err := ApplyCaddyConfig(context.Background(), ssh, names.KubeNamespace(), routes, names)
	if err != nil {
		t.Fatalf("hot reload should succeed, got: %v", err)
	}

	// Should have uploaded ConfigMap (but not Deployment since it exists)
	if len(ssh.Uploads) != 1 {
		t.Errorf("expected 1 upload (configmap only), got %d", len(ssh.Uploads))
	}
}

func TestApplyCaddyConfig_ReloadError(t *testing.T) {
	origDelay := CaddyConfigTimeout
	CaddyConfigTimeout = 10 * time.Millisecond
	defer func() { CaddyConfigTimeout = origDelay }()

	names, _ := utils.NewNames("myapp", "prod")
	routes := []IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"example.com"}},
	}
	caddyfile := generateCaddyfile(routes, names.KubeNamespace())
	expectedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(caddyfile)))

	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "apply --server-side", Result: testutil.MockResult{}},
			{Prefix: "get deployment", Result: testutil.MockResult{Output: []byte("ok")}},
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte("'caddy-abc123'")}},
			{Prefix: "exec caddy-abc123 -- sha256sum", Result: testutil.MockResult{Output: []byte(expectedHash + "  /etc/caddy/Caddyfile")}},
			{Prefix: "exec caddy-abc123 -- caddy reload", Result: testutil.MockResult{Err: fmt.Errorf("reload failed")}},
		},
	}

	err := ApplyCaddyConfig(context.Background(), ssh, names.KubeNamespace(), routes, names)
	if err == nil {
		t.Fatal("expected error on reload failure")
	}
	if !strings.Contains(err.Error(), "caddy reload") {
		t.Errorf("error should mention caddy reload, got: %v", err)
	}
}

func TestGenerateCaddyManifest(t *testing.T) {
	names, err := utils.NewNames("myapp", "production")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	routes := []IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"example.com"}},
	}
	manifest, err := GenerateCaddyManifest(routes, names)
	if err != nil {
		t.Fatalf("GenerateCaddyManifest: %v", err)
	}

	docs := strings.SplitN(manifest, "---", 2)
	if len(docs) != 2 {
		t.Fatalf("expected 2 YAML documents separated by ---, got %d", len(docs))
	}

	// Parse ConfigMap
	var cm corev1.ConfigMap
	if err := sigsyaml.Unmarshal([]byte(docs[0]), &cm); err != nil {
		t.Fatalf("unmarshal ConfigMap: %v", err)
	}
	if cm.Kind != "ConfigMap" {
		t.Errorf("expected Kind ConfigMap, got %q", cm.Kind)
	}
	if cm.Namespace != names.KubeNamespace() {
		t.Errorf("expected namespace %q, got %q", names.KubeNamespace(), cm.Namespace)
	}
	caddyfile, ok := cm.Data["Caddyfile"]
	if !ok || caddyfile == "" {
		t.Error("expected ConfigMap to contain non-empty Caddyfile key")
	}
	if !strings.Contains(caddyfile, "example.com") {
		t.Errorf("expected Caddyfile to contain domain, got:\n%s", caddyfile)
	}

	// Parse Deployment
	var dep appsv1.Deployment
	if err := sigsyaml.Unmarshal([]byte(docs[1]), &dep); err != nil {
		t.Fatalf("unmarshal Deployment: %v", err)
	}
	if dep.Kind != "Deployment" {
		t.Errorf("expected Kind Deployment, got %q", dep.Kind)
	}
	if dep.Namespace != names.KubeNamespace() {
		t.Errorf("expected namespace %q, got %q", names.KubeNamespace(), dep.Namespace)
	}
	if !dep.Spec.Template.Spec.HostNetwork {
		t.Error("expected hostNetwork=true on pod spec")
	}
	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("expected Recreate strategy, got %q", dep.Spec.Strategy.Type)
	}

	// Should NOT have TLS volume
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == "tls" {
			t.Error("deployment should not have TLS volume")
		}
	}
	for _, m := range dep.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == "tls" {
			t.Error("deployment should not have TLS volume mount")
		}
	}
}
