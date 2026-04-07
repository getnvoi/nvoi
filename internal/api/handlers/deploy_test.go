package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/internal/api"
)

const deployYAML = `
servers:
  master:
    type: cx23
    region: fsn1
firewall: default
services:
  db:
    managed: postgres
  web:
    image: nginx
    port: 80
    uses: [db]
domains:
  web: example.com
`

func TestDeploy_CreatesDeploymentWithSteps(t *testing.T) {
	r, db := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// Push config first.
	body := map[string]any{
		"compute_provider": "hetzner",
		"dns_provider":     "cloudflare",
		"config":           deployYAML,
	}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("push config: status = %d, body: %s", w.Code, w.Body.String())
	}

	// Deploy.
	req = authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deploy", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("deploy: status = %d, body: %s", w.Code, w.Body.String())
	}

	var deployment struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Steps  []struct {
			Kind   string `json:"kind"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	decode(t, w, &deployment)

	if deployment.ID == "" {
		t.Error("deployment ID should not be empty")
	}
	if deployment.Status != "pending" {
		t.Errorf("status = %q, want pending", deployment.Status)
	}
	if len(deployment.Steps) == 0 {
		t.Fatal("steps should not be empty")
	}

	// Verify step ordering: first should be instance.set, last should be dns.set.
	first := deployment.Steps[0]
	if first.Kind != "instance.set" {
		t.Errorf("first step = %s, want instance.set", first.Kind)
	}
	last := deployment.Steps[len(deployment.Steps)-1]
	if last.Kind != "ingress.apply" {
		t.Errorf("last step = %s, want ingress.apply", last.Kind)
	}

	// All steps should be pending.
	for _, s := range deployment.Steps {
		if s.Status != "pending" {
			t.Errorf("step %s %s status = %q, want pending", s.Kind, s.Name, s.Status)
		}
	}

	// Verify DB has the deployment.
	var count int64
	db.Model(&api.Deployment{}).Where("repo_id = ?", repoID).Count(&count)
	if count != 1 {
		t.Errorf("deployment count = %d, want 1", count)
	}
}

func TestDeploy_NoConfig(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deploy", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("deploy without config: status = %d, want 400", w.Code)
	}
}

func TestDeploy_GetDeployment(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	// Push + deploy.
	body := map[string]any{"compute_provider": "hetzner", "dns_provider": "cloudflare", "config": deployYAML}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	req = authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deploy", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var created struct{ ID string }
	decode(t, w, &created)

	// Get deployment.
	req = authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/deployments/"+created.ID, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get deployment: status = %d", w.Code)
	}

	var deployment struct {
		ID    string `json:"id"`
		Steps []struct {
			Kind string `json:"kind"`
		} `json:"steps"`
	}
	decode(t, w, &deployment)
	if deployment.ID != created.ID {
		t.Errorf("id = %q, want %q", deployment.ID, created.ID)
	}
	if len(deployment.Steps) == 0 {
		t.Error("steps should be loaded")
	}
}

func TestDeploy_ListDeployments(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	body := map[string]any{"compute_provider": "hetzner", "dns_provider": "cloudflare", "config": deployYAML}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Deploy twice.
	for i := 0; i < 2; i++ {
		req = authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deploy", nil, token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	req = authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/deployments", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status = %d", w.Code)
	}

	var list []struct{ ID string }
	decode(t, w, &list)
	if len(list) != 2 {
		t.Errorf("deployments = %d, want 2", len(list))
	}
}

func TestDeploy_NoInfra_OnlySetSteps(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	body := map[string]any{"compute_provider": "hetzner", "dns_provider": "cloudflare", "config": deployYAML}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Deploy with no real infrastructure — InfraState returns nil.
	// Should produce only set steps, no delete steps.
	req = authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deploy", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("deploy: status = %d, body: %s", w.Code, w.Body.String())
	}

	var deployment struct {
		Steps []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"steps"`
	}
	decode(t, w, &deployment)

	for _, s := range deployment.Steps {
		if strings.HasSuffix(s.Kind, ".delete") {
			t.Errorf("no infra should produce no deletes, got %s %s", s.Kind, s.Name)
		}
	}
}

func TestDeploy_CrossUserIsolation(t *testing.T) {
	rA, db := testRouter(t, "alice")
	tokenA, _, wsA := doLogin(t, rA, "alice")
	repoA := createRepo(t, rA, tokenA, wsA, "app")

	body := map[string]any{"compute_provider": "hetzner", "dns_provider": "cloudflare", "config": deployYAML}
	req := authRequest("POST", "/workspaces/"+wsA+"/repos/"+repoA+"/config", body, tokenA)
	w := httptest.NewRecorder()
	rA.ServeHTTP(w, req)

	req = authRequest("POST", "/workspaces/"+wsA+"/repos/"+repoA+"/deploy", nil, tokenA)
	w = httptest.NewRecorder()
	rA.ServeHTTP(w, req)
	var created struct{ ID string }
	decode(t, w, &created)

	// Bob can't see alice's deployment.
	rB := newRouterWithDB(db, "bob")
	tokenB, _, _ := doLogin(t, rB, "bob")

	req = authRequest("GET", "/workspaces/"+wsA+"/repos/"+repoA+"/deployments/"+created.ID, nil, tokenB)
	w = httptest.NewRecorder()
	rB.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-user: status = %d, want 404", w.Code)
	}
}

func TestRunDeployment_StartsPending(t *testing.T) {
	r, db := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	body := map[string]any{"compute_provider": "hetzner", "dns_provider": "cloudflare", "config": deployYAML}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Create deployment (pending).
	req = authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deploy", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var created struct{ ID string }
	decode(t, w, &created)

	// Run it.
	runPath := "/workspaces/" + wsID + "/repos/" + repoID + "/deployments/" + created.ID + "/run"
	req = authRequest("POST", runPath, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("run: status = %d, body: %s", w.Code, w.Body.String())
	}

	// Give the goroutine time to start — poll until status changes from pending.
	// The executor will fail (no real provider) but it should at least start.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var d api.Deployment
		db.First(&d, "id = ?", created.ID)
		if d.Status != api.DeploymentPending {
			return // success — deployment started
		}
		time.Sleep(10 * time.Millisecond)
	}
	var d api.Deployment
	db.First(&d, "id = ?", created.ID)
	if d.Status == api.DeploymentPending {
		t.Errorf("deployment still pending after /run — executor never started")
	}
}

func TestRunDeployment_RejectsNonPending(t *testing.T) {
	r, db := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	body := map[string]any{"compute_provider": "hetzner", "dns_provider": "cloudflare", "config": deployYAML}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	req = authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deploy", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var created struct{ ID string }
	decode(t, w, &created)

	// Manually mark as running.
	db.Model(&api.Deployment{}).Where("id = ?", created.ID).Update("status", api.DeploymentRunning)

	// /run should reject.
	runPath := "/workspaces/" + wsID + "/repos/" + repoID + "/deployments/" + created.ID + "/run"
	req = authRequest("POST", runPath, nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("run non-pending: status = %d, want 400", w.Code)
	}
}

func TestDeploy_LogsEndpoint(t *testing.T) {
	r, db := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	body := map[string]any{"compute_provider": "hetzner", "dns_provider": "cloudflare", "config": deployYAML}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	req = authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deploy", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var created struct {
		ID    string                `json:"id"`
		Steps []struct{ ID string } `json:"steps"`
	}
	decode(t, w, &created)

	// Manually insert some log lines (simulating executor output).
	if len(created.Steps) > 0 {
		db.Create(&api.DeploymentStepLog{
			DeploymentStepID: created.Steps[0].ID,
			Line:             `{"type":"progress","message":"waiting for SSH"}`,
		})
		db.Create(&api.DeploymentStepLog{
			DeploymentStepID: created.Steps[0].ID,
			Line:             `{"type":"success","message":"SSH ready"}`,
		})
	}

	// Get logs.
	req = authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/deployments/"+created.ID+"/logs", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("logs: status = %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Errorf("content-type = %q, want application/x-ndjson", w.Header().Get("Content-Type"))
	}

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2, body: %q", len(lines), w.Body.String())
	}
	if !strings.Contains(lines[0], "waiting for SSH") {
		t.Errorf("line[0] = %q", lines[0])
	}
	if !strings.Contains(lines[1], "SSH ready") {
		t.Errorf("line[1] = %q", lines[1])
	}
}

func TestDeploy_GetDeploymentNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/deployments/00000000-0000-0000-0000-000000000000", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDeploy_RunNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deployments/00000000-0000-0000-0000-000000000000/run", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDeploy_LogsNotFound(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	req := authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/deployments/00000000-0000-0000-0000-000000000000/logs", nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDeploy_LogsEmpty(t *testing.T) {
	r, _ := testRouter(t, "octocat")
	token, _, wsID := doLogin(t, r, "octocat")
	repoID := createRepo(t, r, token, wsID, "my-app")

	body := map[string]any{"compute_provider": "hetzner", "dns_provider": "cloudflare", "config": deployYAML}
	req := authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/config", body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	req = authRequest("POST", "/workspaces/"+wsID+"/repos/"+repoID+"/deploy", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var created struct{ ID string }
	decode(t, w, &created)

	// No logs yet — should return 200 with empty body.
	req = authRequest("GET", "/workspaces/"+wsID+"/repos/"+repoID+"/deployments/"+created.ID+"/logs", nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Errorf("content-type = %q", w.Header().Get("Content-Type"))
	}
}
