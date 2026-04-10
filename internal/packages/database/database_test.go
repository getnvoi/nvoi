package database

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestValidate_Valid(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{
		Providers: config.ProvidersDef{Compute: "hetzner", Storage: "cloudflare"},
		Volumes:   map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
		Database: map[string]config.DatabaseDef{
			"main": {Image: "postgres:17", Volume: "pgdata"},
		},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx"}},
	}
	if err := pkg.Validate(cfg); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidate_MissingImage(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{
		Providers: config.ProvidersDef{Storage: "cloudflare"},
		Volumes:   map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
		Database:  map[string]config.DatabaseDef{"main": {Volume: "pgdata"}},
	}
	if err := pkg.Validate(cfg); err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestValidate_MissingVolume(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{
		Providers: config.ProvidersDef{Storage: "cloudflare"},
		Volumes:   map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
		Database:  map[string]config.DatabaseDef{"main": {Image: "postgres:17"}},
	}
	if err := pkg.Validate(cfg); err == nil {
		t.Fatal("expected error for missing volume")
	}
}

func TestValidate_VolumeNotDefined(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{
		Providers: config.ProvidersDef{Storage: "cloudflare"},
		Volumes:   map[string]config.VolumeDef{},
		Database:  map[string]config.DatabaseDef{"main": {Image: "postgres:17", Volume: "missing"}},
	}
	if err := pkg.Validate(cfg); err == nil {
		t.Fatal("expected error for undefined volume")
	}
}

func TestValidate_NoStorageProvider(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{
		Providers: config.ProvidersDef{},
		Volumes:   map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
		Database:  map[string]config.DatabaseDef{"main": {Image: "postgres:17", Volume: "pgdata"}},
	}
	if err := pkg.Validate(cfg); err == nil {
		t.Fatal("expected error for missing storage provider")
	}
}

func TestValidate_ServiceCollision(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{
		Providers: config.ProvidersDef{Storage: "cloudflare"},
		Volumes:   map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
		Database:  map[string]config.DatabaseDef{"main": {Image: "postgres:17", Volume: "pgdata"}},
		Services:  map[string]config.ServiceDef{"main-db": {Image: "conflict"}},
	}
	if err := pkg.Validate(cfg); err == nil {
		t.Fatal("expected error for service name collision")
	}
}

func TestValidate_CronCollision(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{
		Providers: config.ProvidersDef{Storage: "cloudflare"},
		Volumes:   map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
		Database:  map[string]config.DatabaseDef{"main": {Image: "postgres:17", Volume: "pgdata"}},
		Services:  map[string]config.ServiceDef{},
		Crons:     map[string]config.CronDef{"main-db-backup": {Image: "conflict", Schedule: "* * * * *"}},
	}
	if err := pkg.Validate(cfg); err == nil {
		t.Fatal("expected error for cron name collision")
	}
}

func TestValidate_MySQLImage(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{
		Providers: config.ProvidersDef{Storage: "cloudflare"},
		Volumes:   map[string]config.VolumeDef{"mysqldata": {Size: 20, Server: "master"}},
		Database: map[string]config.DatabaseDef{
			"analytics": {Image: "mysql:8", Volume: "mysqldata"},
		},
		Services: map[string]config.ServiceDef{},
	}
	if err := pkg.Validate(cfg); err != nil {
		t.Fatalf("mysql should be valid, got: %v", err)
	}
}

func TestActive_WithDatabase(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{
		Database: map[string]config.DatabaseDef{"main": {Image: "postgres:17", Volume: "pgdata"}},
	}
	if !pkg.Active(cfg) {
		t.Error("should be active when database is configured")
	}
}

func TestActive_WithoutDatabase(t *testing.T) {
	pkg := &DatabasePackage{}
	cfg := &config.AppConfig{}
	if pkg.Active(cfg) {
		t.Error("should not be active when no database configured")
	}
}
