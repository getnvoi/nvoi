package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestVolumeSet_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Volumes = map[string]config.VolumeDef{
		"pgdata": {Size: 20, Server: "master"},
	}
	mustValidate(t, cfg)
}

func TestVolumeSet_ZeroSize(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Volumes = map[string]config.VolumeDef{
		"pgdata": {Size: 0, Server: "master"},
	}
	mustFailValidation(t, cfg, "size must be > 0")
}

func TestVolumeSet_MissingServer(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Volumes = map[string]config.VolumeDef{
		"pgdata": {Size: 20},
	}
	mustFailValidation(t, cfg, "server is required")
}

func TestVolumeSet_InvalidServerRef(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Volumes = map[string]config.VolumeDef{
		"pgdata": {Size: 20, Server: "nonexistent"},
	}
	mustFailValidation(t, cfg, "not a defined server")
}

func TestVolumeRemove(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	delete(cfg.Volumes, "pgdata")
	mustValidate(t, cfg)
}
