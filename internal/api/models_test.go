package api

import (
	"testing"

	"gorm.io/gorm"
)

func TestComputeProvider_Valid(t *testing.T) {
	valid := []ComputeProvider{ComputeHetzner, ComputeAWS, ComputeScaleway}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	if ComputeProvider("digitalocean").Valid() {
		t.Error("digitalocean should not be valid")
	}
	if ComputeProvider("").Valid() {
		t.Error("empty should not be valid")
	}
}

func TestDNSProvider_Valid(t *testing.T) {
	valid := []DNSProvider{DNSCloudflare, DNSAWS}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	if DNSProvider("godaddy").Valid() {
		t.Error("godaddy should not be valid")
	}
}

func TestStorageProvider_Valid(t *testing.T) {
	valid := []StorageProvider{StorageCloudflare, StorageAWS}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	if StorageProvider("gcs").Valid() {
		t.Error("gcs should not be valid")
	}
}

func TestBuildProvider_Valid(t *testing.T) {
	valid := []BuildProvider{BuildLocal, BuildDaytona, BuildGitHub}
	for _, p := range valid {
		if !validBuildProviders[p] {
			t.Errorf("%q should be valid", p)
		}
	}
	if validBuildProviders[BuildProvider("circleci")] {
		t.Error("circleci should not be valid")
	}
}

func TestRepoConfig_ValidateProviders(t *testing.T) {
	// Valid: all set.
	rc := &RepoConfig{
		ComputeProvider: ComputeHetzner,
		DNSProvider:     DNSCloudflare,
		StorageProvider: StorageAWS,
		BuildProvider:   BuildDaytona,
	}
	if err := rc.ValidateProviders(); err != nil {
		t.Errorf("all valid: %v", err)
	}

	// Valid: only compute (others optional).
	rc = &RepoConfig{ComputeProvider: ComputeAWS}
	if err := rc.ValidateProviders(); err != nil {
		t.Errorf("compute only: %v", err)
	}

	// Invalid compute.
	rc = &RepoConfig{ComputeProvider: "nope"}
	if err := rc.ValidateProviders(); err == nil {
		t.Error("expected error for invalid compute provider")
	}

	// Invalid DNS.
	rc = &RepoConfig{ComputeProvider: ComputeHetzner, DNSProvider: "nope"}
	if err := rc.ValidateProviders(); err == nil {
		t.Error("expected error for invalid dns provider")
	}

	// Invalid storage.
	rc = &RepoConfig{ComputeProvider: ComputeHetzner, StorageProvider: "nope"}
	if err := rc.ValidateProviders(); err == nil {
		t.Error("expected error for invalid storage provider")
	}

	// Invalid build.
	rc = &RepoConfig{ComputeProvider: ComputeHetzner, BuildProvider: "nope"}
	if err := rc.ValidateProviders(); err == nil {
		t.Error("expected error for invalid build provider")
	}
}

func TestDeployment_Lifecycle(t *testing.T) {
	db := TestDB()

	// Create supporting records.
	user := User{GithubUsername: "test"}
	db.Create(&user)
	ws := Workspace{Name: "default", CreatedBy: user.ID}
	db.Create(&ws)
	db.Create(&WorkspaceUser{UserID: user.ID, WorkspaceID: ws.ID, Role: "owner"})
	repo := Repo{WorkspaceID: ws.ID, Name: "app"}
	db.Create(&repo)
	rc := RepoConfig{RepoID: repo.ID, Version: 1, ComputeProvider: ComputeHetzner, Config: "servers: {}"}
	db.Create(&rc)

	// Create deployment.
	deploy := Deployment{RepoID: repo.ID, RepoConfigID: rc.ID}
	if err := db.Create(&deploy).Error; err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if deploy.ID == "" {
		t.Error("deployment ID should be set")
	}
	if deploy.Status != DeploymentPending {
		t.Errorf("status = %q, want pending", deploy.Status)
	}

	// Add steps.
	step1 := DeploymentStep{DeploymentID: deploy.ID, Position: 1, Kind: "instance.set", Name: "master", Params: `{"type":"cx23"}`}
	step2 := DeploymentStep{DeploymentID: deploy.ID, Position: 2, Kind: "service.set", Name: "web", Params: `{"image":"nginx"}`}
	db.Create(&step1)
	db.Create(&step2)

	// Add log to step1.
	log := DeploymentStepLog{DeploymentStepID: step1.ID, Line: `{"type":"progress","message":"waiting for SSH"}`}
	db.Create(&log)

	// Load deployment with steps and logs.
	var loaded Deployment
	db.Preload("Steps", func(db *gorm.DB) *gorm.DB {
		return db.Order("position")
	}).Preload("Steps.Logs").First(&loaded, "id = ?", deploy.ID)

	if len(loaded.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(loaded.Steps))
	}
	if loaded.Steps[0].Kind != "instance.set" {
		t.Errorf("step[0].kind = %q", loaded.Steps[0].Kind)
	}
	if loaded.Steps[1].Kind != "service.set" {
		t.Errorf("step[1].kind = %q", loaded.Steps[1].Kind)
	}
	if len(loaded.Steps[0].Logs) != 1 {
		t.Fatalf("step[0].logs = %d, want 1", len(loaded.Steps[0].Logs))
	}
	if loaded.Steps[0].Logs[0].Line != `{"type":"progress","message":"waiting for SSH"}` {
		t.Errorf("log line = %q", loaded.Steps[0].Logs[0].Line)
	}
}
