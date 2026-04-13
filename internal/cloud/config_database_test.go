package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestDatabaseSet_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Storage = "cloudflare"
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Database = map[string]config.DatabaseDef{
		"main": {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"},
	}
	mustValidate(t, cfg)
}

func TestDatabaseSet_MissingKind(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Storage = "cloudflare"
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Database = map[string]config.DatabaseDef{
		"main": {Image: "postgres:17", Volume: "pgdata"},
	}
	mustFailValidation(t, cfg, "kind is required")
}

func TestDatabaseSet_MissingImage(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Storage = "cloudflare"
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Database = map[string]config.DatabaseDef{
		"main": {Kind: "postgres", Volume: "pgdata"},
	}
	mustFailValidation(t, cfg, "image is required")
}

func TestDatabaseSet_MissingVolume(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Storage = "cloudflare"
	cfg.Database = map[string]config.DatabaseDef{
		"main": {Kind: "postgres", Image: "postgres:17"},
	}
	mustFailValidation(t, cfg, "volume is required")
}

func TestDatabaseSet_InvalidVolumeRef(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Storage = "cloudflare"
	cfg.Database = map[string]config.DatabaseDef{
		"main": {Kind: "postgres", Image: "postgres:17", Volume: "nonexistent"},
	}
	mustFailValidation(t, cfg, "not a defined volume")
}

func TestDatabaseSet_MissingStorageProvider(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Database = map[string]config.DatabaseDef{
		"main": {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"},
	}
	mustFailValidation(t, cfg, "providers.storage is required")
}

func TestDatabaseRemove(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Database = map[string]config.DatabaseDef{"main": {Kind: "postgres"}}
	delete(cfg.Database, "main")
	mustValidate(t, cfg)
}
