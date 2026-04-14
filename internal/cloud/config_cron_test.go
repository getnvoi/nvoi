package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestCronSet_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Crons = map[string]config.CronDef{
		"cleanup": {Image: "busybox", Schedule: "0 1 * * *", Command: "echo hi"},
	}
	mustValidate(t, cfg)
}

func TestCronSet_MissingImageAndBuild(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Crons = map[string]config.CronDef{
		"cleanup": {Schedule: "0 1 * * *"},
	}
	mustFailValidation(t, cfg, "image or build is required")
}

func TestCronSet_MissingSchedule(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Crons = map[string]config.CronDef{
		"cleanup": {Image: "busybox"},
	}
	mustFailValidation(t, cfg, "schedule is required")
}

func TestCronSet_InvalidBuildRef(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Crons = map[string]config.CronDef{
		"report": {Build: "nonexistent", Schedule: "0 6 * * *"},
	}
	mustFailValidation(t, cfg, "not a defined build target")
}

func TestCronSet_InvalidServerRef(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Crons = map[string]config.CronDef{
		"cleanup": {Image: "busybox", Schedule: "0 1 * * *", Server: "nonexistent"},
	}
	mustFailValidation(t, cfg, "not a defined server")
}

func TestCronRemove(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Crons = map[string]config.CronDef{"cleanup": {Image: "busybox", Schedule: "0 1 * * *"}}
	delete(cfg.Crons, "cleanup")
	mustValidate(t, cfg)
}
