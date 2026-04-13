package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestServerSet_Valid(t *testing.T) {
	cfg := validBaseConfig()
	mustValidate(t, cfg)
}

func TestServerSet_MissingType(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Servers["master"] = config.ServerDef{Region: "nbg1", Role: "master"}
	mustFailValidation(t, cfg, "type is required")
}

func TestServerSet_MissingRegion(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Servers["master"] = config.ServerDef{Type: "cax11", Role: "master"}
	mustFailValidation(t, cfg, "region is required")
}

func TestServerSet_MissingRole(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Servers["master"] = config.ServerDef{Type: "cax11", Region: "nbg1"}
	mustFailValidation(t, cfg, "role is required")
}

func TestServerSet_InvalidRole(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Servers["master"] = config.ServerDef{Type: "cax11", Region: "nbg1", Role: "invalid"}
	mustFailValidation(t, cfg, "must be master or worker")
}

func TestServerSet_DiskOnHetzner(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Servers["master"] = config.ServerDef{Type: "cax11", Region: "nbg1", Role: "master", Disk: 50}
	mustFailValidation(t, cfg, "hetzner does not support custom root disk")
}

func TestServerSet_Worker(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Servers["worker-1"] = config.ServerDef{Type: "cax11", Region: "nbg1", Role: "worker"}
	mustValidate(t, cfg)
}

func TestServerSet_MultipleMasters(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Servers["master2"] = config.ServerDef{Type: "cax11", Region: "nbg1", Role: "master"}
	mustFailValidation(t, cfg, "only one master")
}

func TestServerRemove_NoMaster(t *testing.T) {
	cfg := validBaseConfig()
	delete(cfg.Servers, "master")
	mustFailValidation(t, cfg, "at least one server")
}
