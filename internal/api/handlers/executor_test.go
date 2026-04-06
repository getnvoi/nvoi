package handlers

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/plan"
	"gorm.io/gorm"

	// Providers must be registered for newExecutor to map credentials.
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/daytona"
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"
)

func TestNewExecutor_BuildsFromParams(t *testing.T) {
	db := api.TestDB()
	p := ExecuteParams{
		Repo: &api.Repo{Name: "rails", Environment: "staging"},
		Config: &api.RepoConfig{
			ComputeProvider: api.ComputeHetzner,
			DNSProvider:     api.DNSCloudflare,
			StorageProvider: api.StorageAWS,
			BuildProvider:   api.BuildDaytona,
		},
		Env: map[string]string{
			"HETZNER_TOKEN":      "tok123",
			"CF_API_KEY":         "cfkey",
			"CF_ZONE_ID":        "zone1",
			"DNS_ZONE":           "example.com",
			"AWS_ACCESS_KEY_ID":  "awskey",
			"AWS_SECRET_ACCESS_KEY": "awssecret",
			"CF_ACCOUNT_ID":     "cfacct",
			"DAYTONA_API_KEY":   "daykey",
		},
	}

	e, err := newExecutor(db, p)
	if err != nil {
		t.Fatalf("newExecutor: %v", err)
	}

	if e.cluster.AppName != "rails" {
		t.Errorf("AppName = %q, want rails", e.cluster.AppName)
	}
	if e.cluster.Env != "staging" {
		t.Errorf("Env = %q, want staging", e.cluster.Env)
	}
	if e.cluster.Provider != "hetzner" {
		t.Errorf("Provider = %q, want hetzner", e.cluster.Provider)
	}
	// Compute creds should be schema-mapped: HETZNER_TOKEN → token
	if e.cluster.Credentials["token"] != "tok123" {
		t.Errorf("compute creds[token] = %q, want tok123", e.cluster.Credentials["token"])
	}
	if e.dns.Name != "cloudflare" {
		t.Errorf("DNS = %q, want cloudflare", e.dns.Name)
	}
	if e.storage.Name != "aws" {
		t.Errorf("Storage = %q, want aws", e.storage.Name)
	}
	if e.buildProvider != "daytona" {
		t.Errorf("BuildProvider = %q, want daytona", e.buildProvider)
	}
	if e.builtImages == nil {
		t.Error("builtImages should be initialized")
	}
}

func TestNewExecutor_UnknownProviderFails(t *testing.T) {
	db := api.TestDB()
	p := ExecuteParams{
		Repo:   &api.Repo{Name: "app"},
		Config: &api.RepoConfig{ComputeProvider: "nonexistent"},
		Env:    map[string]string{},
	}

	_, err := newExecutor(db, p)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestExecutor_StepUnknownKind(t *testing.T) {
	e := &executor{builtImages: map[string]string{}}
	err := e.step(context.Background(), "bogus.step", "x", nil)
	if err == nil {
		t.Fatal("expected error for unknown step kind")
	}
	if err.Error() != "unknown step kind: bogus.step" {
		t.Errorf("error = %q", err)
	}
}

func TestExecutor_Run_MarksDeploymentSucceeded(t *testing.T) {
	db := api.TestDB()
	wsID := seedData(t, db)

	repo := api.Repo{WorkspaceID: wsID, Name: "app"}
	db.Create(&repo)
	rc := api.RepoConfig{RepoID: repo.ID, Version: 1, ComputeProvider: api.ComputeHetzner, Config: "{}"}
	db.Create(&rc)

	deployment := api.Deployment{RepoID: repo.ID, RepoConfigID: rc.ID}
	db.Create(&deployment)

	// No steps — deployment should succeed immediately.
	e := &executor{db: db, builtImages: map[string]string{}}
	e.run(context.Background(), &deployment)

	var loaded api.Deployment
	db.First(&loaded, "id = ?", deployment.ID)
	if loaded.Status != api.DeploymentSucceeded {
		t.Errorf("status = %q, want succeeded", loaded.Status)
	}
	if loaded.StartedAt == nil {
		t.Error("started_at should be set")
	}
	if loaded.FinishedAt == nil {
		t.Error("finished_at should be set")
	}
}

func TestExecutor_Run_FailureSkipsRemaining(t *testing.T) {
	db := api.TestDB()
	wsID := seedData(t, db)

	repo := api.Repo{WorkspaceID: wsID, Name: "app"}
	db.Create(&repo)
	rc := api.RepoConfig{RepoID: repo.ID, Version: 1, ComputeProvider: api.ComputeHetzner, Config: "{}"}
	db.Create(&rc)

	deployment := api.Deployment{RepoID: repo.ID, RepoConfigID: rc.ID}
	db.Create(&deployment)

	// Two steps: first is an unknown kind (will fail), second should be skipped.
	db.Create(&api.DeploymentStep{DeploymentID: deployment.ID, Position: 1, Kind: "bogus.fail", Name: "bad"})
	db.Create(&api.DeploymentStep{DeploymentID: deployment.ID, Position: 2, Kind: string(plan.StepSecretSet), Name: "key", Params: `{"value":"v"}`})

	e := &executor{db: db, builtImages: map[string]string{}}
	e.run(context.Background(), &deployment)

	// Deployment should be failed.
	var loaded api.Deployment
	db.First(&loaded, "id = ?", deployment.ID)
	if loaded.Status != api.DeploymentFailed {
		t.Errorf("deployment status = %q, want failed", loaded.Status)
	}

	// First step should be failed, second should be skipped.
	var steps []api.DeploymentStep
	db.Where("deployment_id = ?", deployment.ID).Order("position").Find(&steps)
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
	if steps[0].Status != api.StepStatusFailed {
		t.Errorf("step[0] status = %q, want failed", steps[0].Status)
	}
	if steps[0].Error == "" {
		t.Error("step[0] error should be set")
	}
	if steps[1].Status != api.StepStatusSkipped {
		t.Errorf("step[1] status = %q, want skipped", steps[1].Status)
	}
}

func TestExecutor_Run_WritesLogs(t *testing.T) {
	db := api.TestDB()
	wsID := seedData(t, db)

	repo := api.Repo{WorkspaceID: wsID, Name: "app"}
	db.Create(&repo)
	rc := api.RepoConfig{RepoID: repo.ID, Version: 1, ComputeProvider: api.ComputeHetzner, Config: "{}"}
	db.Create(&rc)

	deployment := api.Deployment{RepoID: repo.ID, RepoConfigID: rc.ID}
	db.Create(&deployment)

	db.Create(&api.DeploymentStep{DeploymentID: deployment.ID, Position: 1, Kind: "bogus.kind", Name: "test"})

	e := &executor{db: db, builtImages: map[string]string{}}
	e.run(context.Background(), &deployment)

	var step api.DeploymentStep
	db.Where("deployment_id = ?", deployment.ID).First(&step)
	if step.StartedAt == nil {
		t.Error("step started_at should be set")
	}
	if step.FinishedAt == nil {
		t.Error("step finished_at should be set")
	}
}

func TestExecutor_BuiltImagesAccumulates(t *testing.T) {
	e := &executor{builtImages: map[string]string{}}

	e.builtImages["web"] = "registry.local/web:abc123"
	if e.builtImages["web"] != "registry.local/web:abc123" {
		t.Errorf("builtImages[web] = %q", e.builtImages["web"])
	}

	e.builtImages["worker"] = "registry.local/worker:def456"
	if len(e.builtImages) != 2 {
		t.Errorf("builtImages count = %d, want 2", len(e.builtImages))
	}
}

// seedData creates a user + workspace for executor tests. Returns workspace ID.
func seedData(t *testing.T, db *gorm.DB) string {
	t.Helper()
	user := api.User{GithubUsername: "test-executor", GithubToken: "ghp_test"}
	db.Create(&user)
	ws := api.Workspace{Name: "default", CreatedBy: user.ID}
	db.Create(&ws)
	db.Create(&api.WorkspaceUser{UserID: user.ID, WorkspaceID: ws.ID, Role: "owner"})
	return ws.ID
}
