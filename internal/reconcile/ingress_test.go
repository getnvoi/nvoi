package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
)

// init clamps Caddy poll loops so reconcile-level tests stay under the
// 2s/package budget. Production timing is still 3s/10m.
func init() {
	kube.SetCaddyTimingForTest(time.Millisecond, 50*time.Millisecond)
}

// ─── Test helpers ────────────────────────────────────────────────────────────

// caddyExecRecorder captures every Exec hitting the Caddy pod so tests can
// assert what the reconciler asked Caddy to do (config swap, cert probe,
// HTTPS probe). Each entry preserves the full Stdin payload so reload tests
// can byte-compare against BuildCaddyConfig output.
type caddyExecRecorder struct {
	mu       sync.Mutex
	calls    []caddyExecCall
	respond  func(req kube.ExecRequest) error
	loadCnt  int32
	certCnt  int32
	httpsCnt int32
}

type caddyExecCall struct {
	Pod   string
	Cmd   string
	Stdin []byte
}

func (r *caddyExecRecorder) handler() func(context.Context, kube.ExecRequest) error {
	return func(_ context.Context, req kube.ExecRequest) error {
		full := strings.Join(req.Command, " ")
		var stdin []byte
		if req.Stdin != nil {
			stdin, _ = io.ReadAll(req.Stdin)
		}
		r.mu.Lock()
		r.calls = append(r.calls, caddyExecCall{Pod: req.Pod, Cmd: full, Stdin: stdin})
		r.mu.Unlock()
		switch {
		case strings.Contains(full, "/load"):
			atomic.AddInt32(&r.loadCnt, 1)
		case strings.Contains(full, "/data/caddy/certificates/"):
			atomic.AddInt32(&r.certCnt, 1)
		case strings.Contains(full, "https://"):
			atomic.AddInt32(&r.httpsCnt, 1)
		}
		if r.respond != nil {
			return r.respond(req)
		}
		return nil
	}
}

func (r *caddyExecRecorder) reloadCalls() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]byte
	for _, c := range r.calls {
		if strings.Contains(c.Cmd, "/load") {
			out = append(out, c.Stdin)
		}
	}
	return out
}

// installCaddyFixture seeds a Ready Caddy Deployment + Pod and wires the
// Exec recorder. Returns the recorder so tests can mutate respond / inspect
// counters.
func installCaddyFixture(t *testing.T, dc *config.DeployContext) *caddyExecRecorder {
	t.Helper()
	kf := kfFor(dc)
	one := int32(1)
	dep := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: kube.CaddyName, Namespace: kube.CaddyNamespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	if _, err := kf.Typed.AppsV1().Deployments(kube.CaddyNamespace).Create(context.Background(), dep, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed caddy deployment: %v", err)
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "caddy-abc",
			Namespace: kube.CaddyNamespace,
			Labels:    map[string]string{"app.kubernetes.io/name": kube.CaddyName},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if _, err := kf.Typed.CoreV1().Pods(kube.CaddyNamespace).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed caddy pod: %v", err)
	}
	rec := &caddyExecRecorder{}
	kf.SetExec(rec.handler())
	return rec
}

// seedService pre-populates the fake with a Service that has Port, so
// kc.GetServicePort succeeds inside the reconciler.
func seedService(t *testing.T, dc *config.DeployContext, svcName string, port int32) {
	t.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: testNS},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: port}}},
	}
	if _, err := kfFor(dc).Typed.CoreV1().Services(testNS).Create(context.Background(), svc, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed service: %v", err)
	}
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestIngress_FreshDeploy_AppliesCaddyAndReloadsConfig(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	rec := installCaddyFixture(t, dc)
	seedService(t, dc, "web", 80)

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string][]string{"web": {"myapp.com"}},
	}

	if err := Ingress(context.Background(), dc, cfg); err != nil {
		t.Fatalf("Ingress: %v", err)
	}

	kf := kfFor(dc)
	if !kf.HasDeployment(kube.CaddyNamespace, kube.CaddyName) {
		t.Errorf("missing caddy deployment")
	}
	if !kf.HasService(kube.CaddyNamespace, kube.CaddyName) {
		t.Errorf("missing caddy service")
	}
	if !kf.HasConfigMap(kube.CaddyNamespace, kube.CaddyConfigMapName) {
		t.Errorf("missing caddy configmap")
	}
	if !kf.HasPVC(kube.CaddyNamespace, kube.CaddyPVCName) {
		t.Errorf("missing caddy pvc")
	}

	if atomic.LoadInt32(&rec.loadCnt) != 1 {
		t.Errorf("expected exactly 1 /load call, got %d", atomic.LoadInt32(&rec.loadCnt))
	}
	if atomic.LoadInt32(&rec.certCnt) < 1 {
		t.Error("expected at least 1 cert wait call")
	}
	if atomic.LoadInt32(&rec.httpsCnt) < 1 {
		t.Error("expected at least 1 HTTPS wait call")
	}

	// Reload payload must equal BuildCaddyConfig output byte-for-byte.
	loads := rec.reloadCalls()
	if len(loads) != 1 {
		t.Fatalf("loads = %d, want 1", len(loads))
	}
	want, err := kube.BuildCaddyConfig(kube.CaddyConfigInput{
		Namespace: testNS,
		Routes:    []kube.CaddyRoute{{Service: "web", Port: 80, Domains: []string{"myapp.com"}}},
	})
	if err != nil {
		t.Fatalf("build expected: %v", err)
	}
	if !bytes.Equal(loads[0], want) {
		t.Errorf("reload payload mismatch:\nwant: %s\ngot:  %s", want, loads[0])
	}
}

func TestIngress_NoDomains_AdminOnlyConfig(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	rec := installCaddyFixture(t, dc)

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}

	if err := Ingress(context.Background(), dc, cfg); err != nil {
		t.Fatalf("Ingress: %v", err)
	}
	if atomic.LoadInt32(&rec.loadCnt) != 1 {
		t.Errorf("expected exactly 1 /load call (admin-only config), got %d", atomic.LoadInt32(&rec.loadCnt))
	}
	if atomic.LoadInt32(&rec.certCnt) != 0 {
		t.Errorf("no domains → no cert waits, got %d", atomic.LoadInt32(&rec.certCnt))
	}
	if atomic.LoadInt32(&rec.httpsCnt) != 0 {
		t.Errorf("no domains → no HTTPS waits, got %d", atomic.LoadInt32(&rec.httpsCnt))
	}
	loads := rec.reloadCalls()
	if len(loads) != 1 {
		t.Fatalf("loads = %d", len(loads))
	}
	var got map[string]any
	if err := json.Unmarshal(loads[0], &got); err != nil {
		t.Fatalf("invalid JSON sent: %v", err)
	}
	apps := got["apps"].(map[string]any)
	http := apps["http"].(map[string]any)
	servers := http["servers"].(map[string]any)
	main := servers["main"].(map[string]any)
	if routes, _ := main["routes"].([]any); len(routes) != 0 {
		t.Errorf("admin-only config must have zero routes, got %v", routes)
	}
}

func TestIngress_Idempotent_SameConfigEachTime(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	rec := installCaddyFixture(t, dc)
	seedService(t, dc, "web", 80)

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string][]string{"web": {"myapp.com"}},
	}

	if err := Ingress(context.Background(), dc, cfg); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := Ingress(context.Background(), dc, cfg); err != nil {
		t.Fatalf("second: %v", err)
	}
	loads := rec.reloadCalls()
	if len(loads) != 2 {
		t.Fatalf("expected 2 /load calls across two reconciles, got %d", len(loads))
	}
	if !bytes.Equal(loads[0], loads[1]) {
		t.Errorf("identical reconciles must POST identical config:\nfirst:  %s\nsecond: %s", loads[0], loads[1])
	}
}

func TestIngress_DomainRemoved_NotInNewConfig(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	rec := installCaddyFixture(t, dc)
	seedService(t, dc, "web", 80)

	cfgBefore := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string][]string{"web": {"myapp.com", "removed.com"}},
	}
	if err := Ingress(context.Background(), dc, cfgBefore); err != nil {
		t.Fatalf("before: %v", err)
	}

	cfgAfter := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string][]string{"web": {"myapp.com"}},
	}
	if err := Ingress(context.Background(), dc, cfgAfter); err != nil {
		t.Fatalf("after: %v", err)
	}

	loads := rec.reloadCalls()
	if len(loads) != 2 {
		t.Fatalf("loads = %d", len(loads))
	}
	if !strings.Contains(string(loads[0]), "removed.com") {
		t.Errorf("first reload should include removed.com:\n%s", loads[0])
	}
	if strings.Contains(string(loads[1]), "removed.com") {
		t.Errorf("second reload must NOT include removed.com:\n%s", loads[1])
	}
}

func TestIngress_CaddyRejectsConfig_FailsWithBody(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	rec := installCaddyFixture(t, dc)
	seedService(t, dc, "web", 80)

	rec.respond = func(req kube.ExecRequest) error {
		if !strings.Contains(strings.Join(req.Command, " "), "/load") {
			return nil
		}
		if req.Stdout != nil {
			_, _ = io.WriteString(req.Stdout, "loading new config: invalid handler 'reverse_typo'")
		}
		return errors.New("exit 22")
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string][]string{"web": {"myapp.com"}},
	}
	err := Ingress(context.Background(), dc, cfg)
	if err == nil {
		t.Fatal("expected error when Caddy rejects config")
	}
	if !strings.Contains(err.Error(), "reverse_typo") {
		t.Errorf("error must surface Caddy rejection body, got: %v", err)
	}
}

func TestIngress_CertOrHTTPSTimeout_Warns_DoesNotFail(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	rec := installCaddyFixture(t, dc)
	seedService(t, dc, "web", 80)

	rec.respond = func(req kube.ExecRequest) error {
		full := strings.Join(req.Command, " ")
		if strings.Contains(full, "/data/caddy/certificates/") {
			return errors.New("cert not yet")
		}
		if strings.Contains(full, "https://") {
			return errors.New("https not yet")
		}
		return nil
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string][]string{"web": {"myapp.com"}},
	}
	if err := Ingress(context.Background(), dc, cfg); err != nil {
		t.Fatalf("cert/HTTPS timeout must not fail the deploy, got: %v", err)
	}
}
