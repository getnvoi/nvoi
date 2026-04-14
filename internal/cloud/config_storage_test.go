package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestStorageSet_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Storage = map[string]config.StorageDef{"releases": {}}
	mustValidate(t, cfg)
}

func TestStorageSet_WithOptions(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Storage = map[string]config.StorageDef{
		"uploads": {CORS: true, ExpireDays: 30},
	}
	mustValidate(t, cfg)
}

func TestStorageRemove(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Storage = map[string]config.StorageDef{"releases": {}}
	delete(cfg.Storage, "releases")
	mustValidate(t, cfg)
}

func TestStorageRemove_ReferencedByService(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Storage = map[string]config.StorageDef{"uploads": {}}
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Storage: []string{"uploads"}}
	mustValidate(t, cfg)

	// Remove storage — service still references it.
	delete(cfg.Storage, "uploads")
	mustFailValidation(t, cfg, "not a defined storage")
}
