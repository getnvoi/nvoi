package managed

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func postgresEnv() map[string]string {
	return map[string]string{"POSTGRES_PASSWORD": "s3cret"}
}

func agentEnv() map[string]string {
	return map[string]string{"NVOI_AGENT_TOKEN": "tok123"}
}

func TestRegistered_NarrowKinds(t *testing.T) {
	got := Registered()
	want := []string{"claude", "postgres"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Registered() = %v, want %v", got, want)
	}
}

func TestCompilePostgresDeterministic(t *testing.T) {
	req := Request{
		Kind:    "postgres",
		Name:    "db",
		Env:     postgresEnv(),
		Context: Context{DefaultVolumeServer: "master"},
	}

	got1, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	got2, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("Compile() not deterministic")
	}
}

func TestCompileClaudeDeterministic(t *testing.T) {
	req := Request{
		Kind:    "claude",
		Name:    "coder",
		Env:     agentEnv(),
		Context: Context{DefaultVolumeServer: "master"},
	}

	got1, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	got2, err := Compile(req)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("Compile() not deterministic")
	}
}

func TestCompilePostgres_MissingCredential(t *testing.T) {
	_, err := Compile(Request{
		Kind:    "postgres",
		Name:    "db",
		Env:     map[string]string{},
		Context: Context{DefaultVolumeServer: "master"},
	})
	if err == nil {
		t.Fatal("expected error for missing POSTGRES_PASSWORD")
	}
	if !strings.Contains(err.Error(), "POSTGRES_PASSWORD") {
		t.Fatalf("error should mention POSTGRES_PASSWORD, got: %v", err)
	}
}

func TestCompileClaude_MissingCredential(t *testing.T) {
	_, err := Compile(Request{
		Kind:    "claude",
		Name:    "coder",
		Env:     map[string]string{},
		Context: Context{DefaultVolumeServer: "master"},
	})
	if err == nil {
		t.Fatal("expected error for missing NVOI_AGENT_TOKEN")
	}
	if !strings.Contains(err.Error(), "NVOI_AGENT_TOKEN") {
		t.Fatalf("error should mention NVOI_AGENT_TOKEN, got: %v", err)
	}
}

func TestGeneratedSecretNamesStable(t *testing.T) {
	got, err := Compile(Request{
		Kind:    "postgres",
		Name:    "db",
		Env:     postgresEnv(),
		Context: Context{DefaultVolumeServer: "master"},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if _, ok := got.Bundle.InternalSecrets["POSTGRES_PASSWORD_DB"]; !ok {
		t.Fatalf("internal secrets = %v, want POSTGRES_PASSWORD_DB", got.Bundle.InternalSecrets)
	}
}

func TestExportedDependencyKeysStable(t *testing.T) {
	got, err := Compile(Request{
		Kind:    "claude",
		Name:    "coder",
		Env:     agentEnv(),
		Context: Context{DefaultVolumeServer: "master"},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := []string{
		"AGENT_CODER_HOST",
		"AGENT_CODER_PORT",
		"AGENT_CODER_TOKEN",
		"AGENT_CODER_URL",
	}
	var keys []string
	for key := range got.Bundle.ExportedSecrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("exported keys = %v, want %v", keys, want)
	}
}

func TestChildResourceNamesStable(t *testing.T) {
	got, err := Compile(Request{
		Kind:    "postgres",
		Name:    "db",
		Env:     postgresEnv(),
		Context: Context{DefaultVolumeServer: "master"},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := []string{"db", "db-backup", "db-backups", "db-data"}
	if !reflect.DeepEqual(got.Bundle.OwnedChildren, want) {
		t.Fatalf("OwnedChildren = %v, want %v", got.Bundle.OwnedChildren, want)
	}
}

func TestPrimitiveOperationsSortedAndDeterministic(t *testing.T) {
	got, err := Compile(Request{
		Kind:    "postgres",
		Name:    "db",
		Env:     postgresEnv(),
		Context: Context{DefaultVolumeServer: "master"},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	gotKinds := make([]string, 0, len(got.Bundle.Operations))
	for _, op := range got.Bundle.Operations {
		gotKinds = append(gotKinds, op.Kind)
	}
	wantKinds := []string{"secret.set", "secret.set", "secret.set", "secret.set", "secret.set", "secret.set", "secret.set", "storage.set", "volume.set", "service.set", "cron.set"}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("operation kinds = %v, want %v", gotKinds, wantKinds)
	}
}

func TestPostgresShape(t *testing.T) {
	shape, err := Shape("postgres", "db")
	if err != nil {
		t.Fatalf("Shape() error = %v", err)
	}
	if shape.Kind != "postgres" {
		t.Errorf("Kind = %q", shape.Kind)
	}
	if shape.RootService != "db" {
		t.Errorf("RootService = %q", shape.RootService)
	}
	want := []string{"db", "db-backup", "db-backups", "db-data"}
	if !reflect.DeepEqual(shape.OwnedChildren, want) {
		t.Errorf("OwnedChildren = %v, want %v", shape.OwnedChildren, want)
	}
	if !reflect.DeepEqual(shape.Crons, []string{"db-backup"}) {
		t.Errorf("Crons = %v", shape.Crons)
	}
	if !reflect.DeepEqual(shape.Services, []string{"db"}) {
		t.Errorf("Services = %v", shape.Services)
	}
	if !reflect.DeepEqual(shape.Storages, []string{"db-backups"}) {
		t.Errorf("Storages = %v", shape.Storages)
	}
	if !reflect.DeepEqual(shape.Volumes, []string{"db-data"}) {
		t.Errorf("Volumes = %v", shape.Volumes)
	}
	wantKeys := []string{
		"POSTGRES_PASSWORD_DB",
		"DATABASE_DB_HOST", "DATABASE_DB_NAME", "DATABASE_DB_PASSWORD",
		"DATABASE_DB_PORT", "DATABASE_DB_URL", "DATABASE_DB_USER",
	}
	if !reflect.DeepEqual(shape.SecretKeys, wantKeys) {
		t.Errorf("SecretKeys = %v, want %v", shape.SecretKeys, wantKeys)
	}
}

func TestClaudeShape(t *testing.T) {
	shape, err := Shape("claude", "coder")
	if err != nil {
		t.Fatalf("Shape() error = %v", err)
	}
	if !reflect.DeepEqual(shape.Services, []string{"coder"}) {
		t.Errorf("Services = %v", shape.Services)
	}
	if !reflect.DeepEqual(shape.Volumes, []string{"coder-data"}) {
		t.Errorf("Volumes = %v", shape.Volumes)
	}
	if len(shape.Crons) != 0 {
		t.Errorf("Crons = %v, want empty", shape.Crons)
	}
	if len(shape.Storages) != 0 {
		t.Errorf("Storages = %v, want empty", shape.Storages)
	}
	wantKeys := []string{
		"NVOI_AGENT_TOKEN_CODER",
		"AGENT_CODER_HOST", "AGENT_CODER_PORT", "AGENT_CODER_TOKEN", "AGENT_CODER_URL",
	}
	if !reflect.DeepEqual(shape.SecretKeys, wantKeys) {
		t.Errorf("SecretKeys = %v, want %v", shape.SecretKeys, wantKeys)
	}
}

func TestPostgresDeleteTargetsMatchBundle(t *testing.T) {
	got, err := Compile(Request{
		Kind:    "postgres",
		Name:    "db",
		Env:     postgresEnv(),
		Context: Context{DefaultVolumeServer: "master"},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	b := got.Bundle

	// Both local delete (iterates bundle.Crons/Services/Storages/Secrets/Volumes)
	// and cloud delete (bundleToDeleteSteps in plan/resolve.go) derive targets from
	// the same Bundle fields. This test proves the bundle declares all owned
	// resources that a delete path needs.

	// Crons.
	if len(b.Crons) != 1 || b.Crons[0].Name != "db-backup" {
		t.Errorf("Crons = %v, want [{Name:db-backup}]", b.Crons)
	}
	// Services.
	if len(b.Services) != 1 || b.Services[0].Name != "db" {
		t.Errorf("Services = %v, want [{Name:db}]", b.Services)
	}
	// Storages.
	if len(b.Storages) != 1 || b.Storages[0].Name != "db-backups" {
		t.Errorf("Storages = %v, want [{Name:db-backups}]", b.Storages)
	}
	// Volumes.
	if len(b.Volumes) != 1 || b.Volumes[0].Name != "db-data" {
		t.Errorf("Volumes = %v, want [{Name:db-data}]", b.Volumes)
	}
	// Secrets (internal + exported).
	allSecrets := map[string]bool{}
	for k := range b.InternalSecrets {
		allSecrets[k] = true
	}
	for k := range b.ExportedSecrets {
		allSecrets[k] = true
	}
	wantSecrets := []string{
		"POSTGRES_PASSWORD_DB",
		"DATABASE_DB_HOST", "DATABASE_DB_NAME", "DATABASE_DB_PASSWORD",
		"DATABASE_DB_PORT", "DATABASE_DB_URL", "DATABASE_DB_USER",
	}
	for _, key := range wantSecrets {
		if !allSecrets[key] {
			t.Errorf("missing secret %q in bundle (have %v)", key, allSecrets)
		}
	}
	if len(allSecrets) != len(wantSecrets) {
		t.Errorf("secret count = %d, want %d", len(allSecrets), len(wantSecrets))
	}
}

func TestClaudeDeleteTargetsMatchBundle(t *testing.T) {
	got, err := Compile(Request{
		Kind:    "claude",
		Name:    "coder",
		Env:     agentEnv(),
		Context: Context{DefaultVolumeServer: "master"},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	b := got.Bundle

	if len(b.Crons) != 0 {
		t.Errorf("agent should have no crons, got %v", b.Crons)
	}
	if len(b.Services) != 1 || b.Services[0].Name != "coder" {
		t.Errorf("Services = %v, want [{Name:coder}]", b.Services)
	}
	if len(b.Storages) != 0 {
		t.Errorf("agent should have no storages, got %v", b.Storages)
	}
	if len(b.Volumes) != 1 || b.Volumes[0].Name != "coder-data" {
		t.Errorf("Volumes = %v, want [{Name:coder-data}]", b.Volumes)
	}
	allSecrets := map[string]bool{}
	for k := range b.InternalSecrets {
		allSecrets[k] = true
	}
	for k := range b.ExportedSecrets {
		allSecrets[k] = true
	}
	wantSecrets := []string{
		"NVOI_AGENT_TOKEN_CODER",
		"AGENT_CODER_HOST", "AGENT_CODER_PORT", "AGENT_CODER_TOKEN", "AGENT_CODER_URL",
	}
	for _, key := range wantSecrets {
		if !allSecrets[key] {
			t.Errorf("missing secret %q in bundle", key)
		}
	}
	if len(allSecrets) != len(wantSecrets) {
		t.Errorf("secret count = %d, want %d", len(allSecrets), len(wantSecrets))
	}
}

func TestOwnershipMetadataComplete(t *testing.T) {
	got, err := Compile(Request{
		Kind:    "claude",
		Name:    "coder",
		Env:     agentEnv(),
		Context: Context{DefaultVolumeServer: "master"},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	for _, op := range got.Bundle.Operations {
		if op.Owner.ManagedKind != "claude" {
			t.Fatalf("Owner.ManagedKind = %q", op.Owner.ManagedKind)
		}
		if op.Owner.RootService != "coder" {
			t.Fatalf("Owner.RootService = %q", op.Owner.RootService)
		}
		if op.Owner.ChildName == "" {
			t.Fatal("Owner.ChildName should not be empty")
		}
	}
}
