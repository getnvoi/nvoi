package config

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestDatabaseNames(t *testing.T) {
	cfg := &AppConfig{
		Database: map[string]DatabaseDef{
			"main":      {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"},
			"analytics": {Kind: "postgres", Image: "postgres:17", Volume: "analytics-data"},
		},
	}
	names := cfg.DatabaseNames()
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
	if names[0] != "analytics" || names[1] != "main" {
		t.Fatalf("names = %v, want [analytics main]", names)
	}
}

func TestDatabaseNames_Empty(t *testing.T) {
	cfg := &AppConfig{}
	names := cfg.DatabaseNames()
	if len(names) != 0 {
		t.Fatalf("len = %d, want 0", len(names))
	}
}

func TestDatabaseNames_Nil(t *testing.T) {
	var cfg *AppConfig
	names := cfg.DatabaseNames()
	if names != nil {
		t.Fatalf("got %v, want nil", names)
	}
}

// ── Resolve tests ─────────────────────────────────────────────────────────────

func TestResolve_VolumeMountPaths(t *testing.T) {
	cfg := &AppConfig{
		App: "myapp",
		Env: "production",
		Volumes: map[string]VolumeDef{
			"pgdata":  {Size: 20, Server: "master"},
			"uploads": {Size: 50, Server: "worker"},
		},
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}

	names, _ := utils.NewNames("myapp", "production")

	// Each volume's MountPath must equal names.VolumeMountPath with the volume config key.
	for volName, vol := range cfg.Volumes {
		want := names.VolumeMountPath(volName)
		if vol.MountPath != want {
			t.Errorf("Volumes[%q].MountPath = %q, want %q", volName, vol.MountPath, want)
		}
	}

	// Spot-check the actual path format
	if got := cfg.Volumes["pgdata"].MountPath; got != "/mnt/data/nvoi-myapp-production-pgdata" {
		t.Errorf("pgdata.MountPath = %q, want /mnt/data/nvoi-myapp-production-pgdata", got)
	}
}

func TestResolve_DatabaseNames(t *testing.T) {
	cfg := &AppConfig{
		App: "myapp",
		Env: "production",
		Volumes: map[string]VolumeDef{
			"pgdata":    {Size: 20, Server: "master"},
			"mysqldata": {Size: 30, Server: "worker"},
		},
		Database: map[string]DatabaseDef{
			"main":      {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"},
			"analytics": {Kind: "mysql", Image: "mysql:8", Volume: "mysqldata"},
		},
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		dbName           string
		wantService      string
		wantSecret       string
		wantBackupCron   string
		wantBackupBucket string
		wantVolumePath   string
	}{
		{
			dbName:           "main",
			wantService:      "main-db",
			wantSecret:       "main-db-credentials",
			wantBackupCron:   "main-db-backup",
			wantBackupBucket: "main-db-backups",
			wantVolumePath:   "/mnt/data/nvoi-myapp-production-pgdata",
		},
		{
			dbName:           "analytics",
			wantService:      "analytics-db",
			wantSecret:       "analytics-db-credentials",
			wantBackupCron:   "analytics-db-backup",
			wantBackupBucket: "analytics-db-backups",
			wantVolumePath:   "/mnt/data/nvoi-myapp-production-mysqldata",
		},
	}

	for _, tt := range tests {
		db := cfg.Database[tt.dbName]
		if db.ServiceName != tt.wantService {
			t.Errorf("Database[%q].ServiceName = %q, want %q", tt.dbName, db.ServiceName, tt.wantService)
		}
		if db.SecretName != tt.wantSecret {
			t.Errorf("Database[%q].SecretName = %q, want %q", tt.dbName, db.SecretName, tt.wantSecret)
		}
		if db.BackupCronName != tt.wantBackupCron {
			t.Errorf("Database[%q].BackupCronName = %q, want %q", tt.dbName, db.BackupCronName, tt.wantBackupCron)
		}
		if db.BackupBucket != tt.wantBackupBucket {
			t.Errorf("Database[%q].BackupBucket = %q, want %q", tt.dbName, db.BackupBucket, tt.wantBackupBucket)
		}
		if db.VolumeMountPath != tt.wantVolumePath {
			t.Errorf("Database[%q].VolumeMountPath = %q, want %q", tt.dbName, db.VolumeMountPath, tt.wantVolumePath)
		}
	}
}

func TestResolve_DatabaseVolumeMountPath_MatchesVolumeConfig(t *testing.T) {
	// The critical invariant: database's VolumeMountPath MUST equal
	// the referenced volume's MountPath. This is the bug we're preventing.
	cfg := &AppConfig{
		App: "myapp",
		Env: "production",
		Volumes: map[string]VolumeDef{
			"pgdata": {Size: 20, Server: "master"},
		},
		Database: map[string]DatabaseDef{
			"main": {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"},
		},
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}

	db := cfg.Database["main"]
	vol := cfg.Volumes["pgdata"]

	if db.VolumeMountPath != vol.MountPath {
		t.Fatalf("INVARIANT VIOLATION: Database[main].VolumeMountPath = %q, but Volumes[pgdata].MountPath = %q — these MUST match",
			db.VolumeMountPath, vol.MountPath)
	}
}

func TestResolve_ConsistentWithUtilsFunctions(t *testing.T) {
	// Resolve() must produce the same values as the utils derivation functions.
	// If either changes, this test catches the divergence.
	cfg := &AppConfig{
		App: "myapp",
		Env: "production",
		Volumes: map[string]VolumeDef{
			"pgdata": {Size: 20, Server: "master"},
		},
		Database: map[string]DatabaseDef{
			"main": {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"},
		},
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}

	db := cfg.Database["main"]
	if db.ServiceName != utils.DatabaseServiceName("main") {
		t.Errorf("ServiceName = %q, utils.DatabaseServiceName = %q", db.ServiceName, utils.DatabaseServiceName("main"))
	}
	if db.SecretName != utils.DatabaseSecretName("main") {
		t.Errorf("SecretName = %q, utils.DatabaseSecretName = %q", db.SecretName, utils.DatabaseSecretName("main"))
	}
	if db.BackupCronName != utils.DatabaseBackupCronName("main") {
		t.Errorf("BackupCronName = %q, utils.DatabaseBackupCronName = %q", db.BackupCronName, utils.DatabaseBackupCronName("main"))
	}
	if db.BackupBucket != utils.DatabaseBackupBucket("main") {
		t.Errorf("BackupBucket = %q, utils.DatabaseBackupBucket = %q", db.BackupBucket, utils.DatabaseBackupBucket("main"))
	}
}

func TestResolve_NoDatabaseNoVolume(t *testing.T) {
	// Resolve on config with no volumes and no databases should succeed.
	cfg := &AppConfig{
		App: "myapp",
		Env: "production",
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatalf("Resolve failed on empty config: %v", err)
	}
}

func TestResolve_MissingAppEnv(t *testing.T) {
	cfg := &AppConfig{}
	if err := cfg.Resolve(); err == nil {
		t.Fatal("expected error when app/env are empty")
	}
}

// ── Secrets provider YAML parsing ────────────────────────────────────────────

func TestParseAppConfig_SecretsProvider(t *testing.T) {
	yaml := `
app: myapp
env: prod
providers:
  compute: hetzner
  secrets: doppler
servers:
  master:
    type: cx23
    region: fsn1
    role: master
services:
  web:
    image: nginx
`
	cfg, err := ParseAppConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Providers.Secrets != "doppler" {
		t.Errorf("Providers.Secrets = %q, want doppler", cfg.Providers.Secrets)
	}
}

func TestParseAppConfig_NoSecretsProvider(t *testing.T) {
	yaml := `
app: myapp
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cx23
    region: fsn1
    role: master
services:
  web:
    image: nginx
`
	cfg, err := ParseAppConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Providers.Secrets != "" {
		t.Fatalf("Providers.Secrets should be empty, got %q", cfg.Providers.Secrets)
	}
}
