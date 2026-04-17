package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// ─── BuildCaddyConfig ────────────────────────────────────────────────────────

func TestBuildCaddyConfig_Empty(t *testing.T) {
	out, err := BuildCaddyConfig(CaddyConfigInput{Namespace: "nvoi-myapp-prod"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	admin, _ := got["admin"].(map[string]any)
	if admin["listen"] != CaddyAdminListen {
		t.Errorf("admin.listen = %v, want %s", admin["listen"], CaddyAdminListen)
	}
	apps, _ := got["apps"].(map[string]any)
	http, _ := apps["http"].(map[string]any)
	servers, _ := http["servers"].(map[string]any)
	main, _ := servers["main"].(map[string]any)
	listen, _ := main["listen"].([]any)
	if len(listen) != 2 || listen[0] != ":80" || listen[1] != ":443" {
		t.Errorf("main.listen = %v, want [:80, :443]", listen)
	}
	routes, _ := main["routes"].([]any)
	if len(routes) != 0 {
		t.Errorf("routes should be empty, got %v", routes)
	}
	if strings.Contains(string(out), "secretName") {
		t.Errorf("REGRESSION — Traefik-era secretName must never appear: %s", out)
	}
	if strings.Contains(string(out), "traefik") {
		t.Errorf("REGRESSION — Traefik-era token must never appear: %s", out)
	}
}

func TestBuildCaddyConfig_SingleDomain(t *testing.T) {
	out, err := BuildCaddyConfig(CaddyConfigInput{
		Namespace: "nvoi-myapp-prod",
		Routes: []CaddyRoute{
			{Service: "api", Port: 8080, Domains: []string{"api.example.com"}},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	var cfg caddyConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	main := cfg.Apps.HTTP.Servers["main"]
	if len(main.Routes) != 1 {
		t.Fatalf("routes len = %d, want 1", len(main.Routes))
	}
	rt := main.Routes[0]
	if len(rt.Match) != 1 || len(rt.Match[0].Host) != 1 || rt.Match[0].Host[0] != "api.example.com" {
		t.Errorf("route match = %+v", rt.Match)
	}
	if len(rt.Handle) != 1 || rt.Handle[0].Handler != "reverse_proxy" {
		t.Fatalf("handle = %+v", rt.Handle)
	}
	dial := rt.Handle[0].Upstreams[0].Dial
	want := "api.nvoi-myapp-prod.svc.cluster.local:8080"
	if dial != want {
		t.Errorf("dial = %q, want %q", dial, want)
	}
	if len(cfg.Apps.TLS.Automation.Policies) != 1 {
		t.Fatalf("tls policies = %+v", cfg.Apps.TLS.Automation.Policies)
	}
	policy := cfg.Apps.TLS.Automation.Policies[0]
	if len(policy.Subjects) != 1 || policy.Subjects[0] != "api.example.com" {
		t.Errorf("subjects = %v", policy.Subjects)
	}
	if len(policy.Issuers) != 1 || policy.Issuers[0].Module != "acme" {
		t.Fatalf("issuers = %+v", policy.Issuers)
	}
	if policy.Issuers[0].Email != "acme@api.example.com" {
		t.Errorf("derived email = %q", policy.Issuers[0].Email)
	}
}

func TestBuildCaddyConfig_MultipleServicesDeterministicOrder(t *testing.T) {
	in := CaddyConfigInput{
		Namespace: "ns",
		Routes: []CaddyRoute{
			// Out-of-order on purpose; output must sort by Service.
			{Service: "web", Port: 3000, Domains: []string{"www.example.com", "example.com"}},
			{Service: "api", Port: 8080, Domains: []string{"api.example.com"}},
		},
		ACMEEmail: "ops@example.com",
	}
	first, err := BuildCaddyConfig(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	second, err := BuildCaddyConfig(in)
	if err != nil {
		t.Fatalf("build (2): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("non-deterministic output:\nfirst:  %s\nsecond: %s", first, second)
	}

	var cfg caddyConfig
	if err := json.Unmarshal(first, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	main := cfg.Apps.HTTP.Servers["main"]
	if len(main.Routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(main.Routes))
	}
	// api < web after sort.
	if main.Routes[0].Match[0].Host[0] != "api.example.com" {
		t.Errorf("first route should be api, got %v", main.Routes[0].Match[0].Host)
	}
	if main.Routes[1].Match[0].Host[0] != "www.example.com" {
		t.Errorf("second route should preserve domain order: got %v", main.Routes[1].Match[0].Host)
	}
	policy := cfg.Apps.TLS.Automation.Policies[0]
	if policy.Issuers[0].Email != "ops@example.com" {
		t.Errorf("explicit email overridden: %q", policy.Issuers[0].Email)
	}
	wantSubjects := []string{"api.example.com", "www.example.com", "example.com"}
	if len(policy.Subjects) != len(wantSubjects) {
		t.Fatalf("subjects = %v, want %v", policy.Subjects, wantSubjects)
	}
	for i, s := range wantSubjects {
		if policy.Subjects[i] != s {
			t.Errorf("subjects[%d] = %q, want %q", i, policy.Subjects[i], s)
		}
	}
}

func TestBuildCaddyConfig_MissingPort(t *testing.T) {
	_, err := BuildCaddyConfig(CaddyConfigInput{
		Namespace: "ns",
		Routes:    []CaddyRoute{{Service: "api", Port: 0, Domains: []string{"x.com"}}},
	})
	if err == nil || !strings.Contains(err.Error(), `port`) {
		t.Errorf("expected port error, got: %v", err)
	}
}

func TestBuildCaddyConfig_NoNamespace(t *testing.T) {
	_, err := BuildCaddyConfig(CaddyConfigInput{Routes: []CaddyRoute{{Service: "x", Port: 1, Domains: []string{"a.com"}}}})
	if err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("expected namespace error, got: %v", err)
	}
}

// ─── Manifest builders ───────────────────────────────────────────────────────

func TestBuildCaddyDeployment_Shape(t *testing.T) {
	dep := buildCaddyDeployment()
	if dep.Namespace != CaddyNamespace || dep.Name != CaddyName {
		t.Errorf("name/ns = %s/%s", dep.Name, dep.Namespace)
	}
	tpl := dep.Spec.Template.Spec
	if tpl.NodeSelector[utils.LabelNvoiRole] != utils.RoleMaster {
		t.Errorf("nodeSelector = %v", tpl.NodeSelector)
	}
	if len(tpl.Containers) != 1 {
		t.Fatalf("containers = %d", len(tpl.Containers))
	}
	cnt := tpl.Containers[0]
	if cnt.Image != CaddyImage {
		t.Errorf("image = %q, want %q (regression: floating tag)", cnt.Image, CaddyImage)
	}
	if cnt.ReadinessProbe == nil || cnt.ReadinessProbe.TCPSocket == nil {
		t.Errorf("readiness probe must be TCP on :80, got %+v", cnt.ReadinessProbe)
	}
	if cnt.ReadinessProbe.TCPSocket.Port.IntValue() != 80 {
		t.Errorf("readiness port = %v, want 80", cnt.ReadinessProbe.TCPSocket.Port)
	}
	httpFound, httpsFound := false, false
	for _, p := range cnt.Ports {
		switch p.ContainerPort {
		case 80:
			httpFound = true
			if p.HostPort != 80 {
				t.Errorf("port 80 hostPort = %d, want 80", p.HostPort)
			}
		case 443:
			httpsFound = true
			if p.HostPort != 443 {
				t.Errorf("port 443 hostPort = %d, want 443", p.HostPort)
			}
		}
	}
	if !httpFound || !httpsFound {
		t.Errorf("missing host ports — got %+v", cnt.Ports)
	}
}

func TestBuildCaddyService_AdminNotExposed(t *testing.T) {
	svc := buildCaddyService()
	for _, p := range svc.Spec.Ports {
		if p.Port == 2019 || p.TargetPort.IntValue() == 2019 {
			t.Errorf("admin port 2019 must NEVER appear in Service: %+v", svc.Spec.Ports)
		}
	}
}

func TestBuildCaddyConfigMap_SeedIsAdminOnly(t *testing.T) {
	cm := buildCaddyConfigMap()
	data, ok := cm.Data[CaddyConfigKey]
	if !ok {
		t.Fatalf("ConfigMap missing key %s", CaddyConfigKey)
	}
	var seed map[string]any
	if err := json.Unmarshal([]byte(data), &seed); err != nil {
		t.Fatalf("seed not valid JSON: %v", err)
	}
	if _, ok := seed["apps"]; ok {
		t.Errorf("seed must NOT include apps — admin only on first boot, got: %s", data)
	}
}

func TestBuildCaddyPVC_AccessMode(t *testing.T) {
	pvc := buildCaddyPVC()
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("access mode = %v, want RWO (single-master, hostPort, single replica)", pvc.Spec.AccessModes)
	}
	if pvc.Spec.Resources.Requests.Storage().String() != "1Gi" {
		t.Errorf("size = %s, want 1Gi", pvc.Spec.Resources.Requests.Storage())
	}
}

// ─── EnsureCaddy ─────────────────────────────────────────────────────────────

func TestEnsureCaddy_AppliesAllFour(t *testing.T) {
	c := newTestClient()
	cleanup := SetCaddyTimingForTest(time.Millisecond, 200*time.Millisecond)
	defer cleanup()
	// Pre-seed a Ready Deployment so waitForCaddyReady doesn't poll-timeout —
	// we still want the Apply path to update the status to readyReplicas == 1.
	go func() {
		// Background Updater: once Apply creates the Deployment, mutate its
		// status to make it Ready. We poll until it appears.
		for i := 0; i < 200; i++ {
			dep, err := c.cs.AppsV1().Deployments(CaddyNamespace).Get(context.Background(), CaddyName, metav1.GetOptions{})
			if err == nil {
				dep.Status.ReadyReplicas = 1
				_, _ = c.cs.AppsV1().Deployments(CaddyNamespace).UpdateStatus(context.Background(), dep, metav1.UpdateOptions{})
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	if err := c.EnsureCaddy(context.Background()); err != nil {
		t.Fatalf("EnsureCaddy: %v", err)
	}
	for _, want := range []struct{ kind, name string }{
		{"deployment", CaddyName},
		{"service", CaddyName},
		{"configmap", CaddyConfigMapName},
		{"pvc", CaddyPVCName},
	} {
		switch want.kind {
		case "deployment":
			if _, err := c.cs.AppsV1().Deployments(CaddyNamespace).Get(context.Background(), want.name, metav1.GetOptions{}); err != nil {
				t.Errorf("missing %s/%s: %v", want.kind, want.name, err)
			}
		case "service":
			if _, err := c.cs.CoreV1().Services(CaddyNamespace).Get(context.Background(), want.name, metav1.GetOptions{}); err != nil {
				t.Errorf("missing %s/%s: %v", want.kind, want.name, err)
			}
		case "configmap":
			if _, err := c.cs.CoreV1().ConfigMaps(CaddyNamespace).Get(context.Background(), want.name, metav1.GetOptions{}); err != nil {
				t.Errorf("missing %s/%s: %v", want.kind, want.name, err)
			}
		case "pvc":
			if _, err := c.cs.CoreV1().PersistentVolumeClaims(CaddyNamespace).Get(context.Background(), want.name, metav1.GetOptions{}); err != nil {
				t.Errorf("missing %s/%s: %v", want.kind, want.name, err)
			}
		}
	}
}

func TestEnsureCaddy_Idempotent(t *testing.T) {
	c := newTestClient()
	cleanup := SetCaddyTimingForTest(time.Millisecond, 200*time.Millisecond)
	defer cleanup()
	go updateCaddyDeploymentReady(c)
	if err := c.EnsureCaddy(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	go updateCaddyDeploymentReady(c)
	if err := c.EnsureCaddy(context.Background()); err != nil {
		t.Fatalf("second: %v", err)
	}
}

func updateCaddyDeploymentReady(c *Client) {
	for i := 0; i < 200; i++ {
		dep, err := c.cs.AppsV1().Deployments(CaddyNamespace).Get(context.Background(), CaddyName, metav1.GetOptions{})
		if err == nil {
			dep.Status.ReadyReplicas = 1
			_, _ = c.cs.AppsV1().Deployments(CaddyNamespace).UpdateStatus(context.Background(), dep, metav1.UpdateOptions{})
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// ─── ReloadCaddyConfig ───────────────────────────────────────────────────────

func TestReloadCaddyConfig_SendsExactJSONToAdminAPI(t *testing.T) {
	c := newTestClient(caddyReadyPod())

	var captured bytes.Buffer
	var cmd []string
	var execNS, execPod string
	c.ExecFunc = func(_ context.Context, req ExecRequest) error {
		execNS = req.Namespace
		execPod = req.Pod
		cmd = req.Command
		if req.Stdin != nil {
			_, _ = io.Copy(&captured, req.Stdin)
		}
		return nil
	}

	want := []byte(`{"admin":{"listen":"localhost:2019"}}`)
	if err := c.ReloadCaddyConfig(context.Background(), want); err != nil {
		t.Fatalf("ReloadCaddyConfig: %v", err)
	}
	if !bytes.Equal(captured.Bytes(), want) {
		t.Errorf("stdin sent != input:\nwant: %s\ngot:  %s", want, captured.String())
	}
	if execNS != CaddyNamespace || execPod == "" {
		t.Errorf("exec target = %s/%s, want kube-system/<caddy pod>", execNS, execPod)
	}
	full := strings.Join(cmd, " ")
	if !strings.Contains(full, "/load") || !strings.Contains(full, CaddyAdminListen) {
		t.Errorf("exec command must POST to admin /load, got: %s", full)
	}
	if !strings.Contains(full, "--data-binary @-") {
		t.Errorf("exec command must read body from stdin, got: %s", full)
	}
}

func TestReloadCaddyConfig_RejectionSurfacesBody(t *testing.T) {
	c := newTestClient(caddyReadyPod())
	c.ExecFunc = func(_ context.Context, req ExecRequest) error {
		// Caddy validation failure: body printed via --fail-with-body, then
		// non-zero exit.
		if req.Stdout != nil {
			_, _ = io.WriteString(req.Stdout, "loading new config: parse: invalid module 'reverse_proxy_typo'")
		}
		return errors.New("command terminated with exit code 22")
	}
	err := c.ReloadCaddyConfig(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error when Caddy rejects config")
	}
	if !strings.Contains(err.Error(), "reverse_proxy_typo") {
		t.Errorf("error must include Caddy's rejection body, got: %v", err)
	}
}

// ─── WaitForCaddyCert / WaitForCaddyHTTPS ───────────────────────────────────

func TestWaitForCaddyCert_Succeeds(t *testing.T) {
	c := newTestClient(caddyReadyPod())
	cleanup := SetCaddyTimingForTest(time.Millisecond, 200*time.Millisecond)
	defer cleanup()

	var calls int32
	c.ExecFunc = func(_ context.Context, req ExecRequest) error {
		atomic.AddInt32(&calls, 1)
		full := strings.Join(req.Command, " ")
		if !strings.Contains(full, "/data/caddy/certificates/") {
			t.Errorf("cert path absent from cmd: %s", full)
		}
		if !strings.Contains(full, "example.com") {
			t.Errorf("domain absent from cmd: %s", full)
		}
		return nil
	}
	if err := c.WaitForCaddyCert(context.Background(), "example.com"); err != nil {
		t.Fatalf("WaitForCaddyCert: %v", err)
	}
	if atomic.LoadInt32(&calls) < 1 {
		t.Error("Exec should have been called at least once")
	}
}

func TestWaitForCaddyCert_TimesOut(t *testing.T) {
	c := newTestClient(caddyReadyPod())
	cleanup := SetCaddyTimingForTest(time.Millisecond, 20*time.Millisecond)
	defer cleanup()

	c.ExecFunc = func(_ context.Context, _ ExecRequest) error {
		return errors.New("test: not yet")
	}
	err := c.WaitForCaddyCert(context.Background(), "example.com")
	if err == nil {
		t.Fatal("expected timeout, got nil")
	}
	if !errors.Is(err, utils.ErrTimeout) {
		t.Errorf("expected ErrTimeout, got: %v", err)
	}
}

func TestWaitForCaddyHTTPS_DefaultsHealthPath(t *testing.T) {
	c := newTestClient(caddyReadyPod())
	cleanup := SetCaddyTimingForTest(time.Millisecond, 200*time.Millisecond)
	defer cleanup()

	c.ExecFunc = func(_ context.Context, req ExecRequest) error {
		full := strings.Join(req.Command, " ")
		if !strings.Contains(full, "https://example.com/") {
			t.Errorf("must default health path to /, got: %s", full)
		}
		return nil
	}
	if err := c.WaitForCaddyHTTPS(context.Background(), "example.com", ""); err != nil {
		t.Fatalf("WaitForCaddyHTTPS: %v", err)
	}
}

func TestWaitForCaddyHTTPS_HonorsCustomHealthPath(t *testing.T) {
	c := newTestClient(caddyReadyPod())
	cleanup := SetCaddyTimingForTest(time.Millisecond, 200*time.Millisecond)
	defer cleanup()
	c.ExecFunc = func(_ context.Context, req ExecRequest) error {
		full := strings.Join(req.Command, " ")
		if !strings.Contains(full, "https://example.com/healthz") {
			t.Errorf("path not respected, got: %s", full)
		}
		return nil
	}
	if err := c.WaitForCaddyHTTPS(context.Background(), "example.com", "/healthz"); err != nil {
		t.Fatalf("WaitForCaddyHTTPS: %v", err)
	}
}

// ─── GetCaddyRoutes ──────────────────────────────────────────────────────────

func TestGetCaddyRoutes_ParsesLiveAdminConfig(t *testing.T) {
	c := newTestClient(caddyReadyPod())
	c.ExecFunc = func(_ context.Context, req ExecRequest) error {
		if req.Stdout != nil {
			_, _ = io.WriteString(req.Stdout, `[
				{"match":[{"host":["api.example.com"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"api.nvoi-myapp-prod.svc.cluster.local:8080"}]}]},
				{"match":[{"host":["www.example.com","example.com"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"web.nvoi-myapp-prod.svc.cluster.local:3000"}]}]}
			]`)
		}
		return nil
	}
	routes, err := c.GetCaddyRoutes(context.Background())
	if err != nil {
		t.Fatalf("GetCaddyRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	if routes[0].Service != "api" || routes[0].Port != 8080 {
		t.Errorf("api route: %+v", routes[0])
	}
	if routes[1].Service != "web" || routes[1].Port != 3000 {
		t.Errorf("web route: %+v", routes[1])
	}
	if len(routes[1].Domains) != 2 || routes[1].Domains[0] != "www.example.com" {
		t.Errorf("web domains: %+v", routes[1].Domains)
	}
}

func TestGetCaddyRoutes_NoCaddyPod_NoError(t *testing.T) {
	c := newTestClient() // no pod
	routes, err := c.GetCaddyRoutes(context.Background())
	if err != nil {
		t.Fatalf("expected nil error when caddy isn't running, got: %v", err)
	}
	if routes != nil {
		t.Errorf("expected nil routes, got %v", routes)
	}
}

func TestGetCaddyRoutes_AdminUnreachable_NoError(t *testing.T) {
	c := newTestClient(caddyReadyPod())
	c.ExecFunc = func(_ context.Context, _ ExecRequest) error {
		return errors.New("connection refused")
	}
	routes, err := c.GetCaddyRoutes(context.Background())
	if err != nil {
		t.Fatalf("admin unreachable must degrade silently, got: %v", err)
	}
	if routes != nil {
		t.Errorf("expected nil routes, got %v", routes)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func caddyReadyPod() *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "caddy-abc123",
			Namespace: CaddyNamespace,
			Labels:    map[string]string{utils.LabelAppName: CaddyName},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
}
