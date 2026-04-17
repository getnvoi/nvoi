package reconcile

import (
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

// svc returns a minimal ServiceDef with the given deps.
func svc(deps ...string) config.ServiceDef {
	return config.ServiceDef{Image: "x", DependsOn: deps}
}

// indexOf returns the position of name in the sorted list, or -1 if absent.
func indexOf(list []string, name string) int {
	for i, n := range list {
		if n == name {
			return i
		}
	}
	return -1
}

func TestTopoSort_NoDeps_Alphabetical(t *testing.T) {
	got := topoSortServices(map[string]config.ServiceDef{
		"web": svc(), "api": svc(), "worker": svc(),
	})
	want := []string{"api", "web", "worker"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTopoSort_SimpleDep_DepFirst(t *testing.T) {
	got := topoSortServices(map[string]config.ServiceDef{
		"api":      svc("postgres"),
		"postgres": svc(),
	})
	if indexOf(got, "postgres") > indexOf(got, "api") {
		t.Errorf("postgres must come before api, got %v", got)
	}
}

func TestTopoSort_ChainedDeps(t *testing.T) {
	// api → cache → postgres → (nothing)
	got := topoSortServices(map[string]config.ServiceDef{
		"api":      svc("cache"),
		"cache":    svc("postgres"),
		"postgres": svc(),
	})
	pgIdx := indexOf(got, "postgres")
	cacheIdx := indexOf(got, "cache")
	apiIdx := indexOf(got, "api")
	if !(pgIdx < cacheIdx && cacheIdx < apiIdx) {
		t.Errorf("expected postgres < cache < api, got %v", got)
	}
}

func TestTopoSort_Diamond(t *testing.T) {
	//   postgres
	//    /    \
	//   a      b
	//    \    /
	//     web
	got := topoSortServices(map[string]config.ServiceDef{
		"postgres": svc(),
		"a":        svc("postgres"),
		"b":        svc("postgres"),
		"web":      svc("a", "b"),
	})
	pgIdx := indexOf(got, "postgres")
	aIdx := indexOf(got, "a")
	bIdx := indexOf(got, "b")
	wIdx := indexOf(got, "web")
	if !(pgIdx < aIdx && pgIdx < bIdx && aIdx < wIdx && bIdx < wIdx) {
		t.Errorf("diamond ordering wrong: %v", got)
	}
}

func TestTopoSort_UnknownDepIgnored(t *testing.T) {
	// validate.go rejects unknown deps before reconcile runs. topsort
	// is defensive: it must not crash or loop if an unknown slipped past.
	got := topoSortServices(map[string]config.ServiceDef{
		"api": svc("nonexistent"),
	})
	if len(got) != 1 || got[0] != "api" {
		t.Errorf("unknown dep must be ignored gracefully, got %v", got)
	}
}

func TestFindDependencyCycle_None(t *testing.T) {
	if got := findDependencyCycle(map[string]config.ServiceDef{
		"api":      svc("postgres"),
		"postgres": svc(),
	}); got != "" {
		t.Errorf("acyclic graph reported as cycle: %s", got)
	}
}

func TestFindDependencyCycle_Direct(t *testing.T) {
	// api ↔ postgres
	got := findDependencyCycle(map[string]config.ServiceDef{
		"api":      svc("postgres"),
		"postgres": svc("api"),
	})
	if got == "" {
		t.Fatal("expected cycle detected")
	}
	if !strings.Contains(got, "api") || !strings.Contains(got, "postgres") {
		t.Errorf("cycle description should mention both members: %s", got)
	}
}

func TestFindDependencyCycle_Self(t *testing.T) {
	// self-ref is flagged separately in validate; findDependencyCycle
	// should also detect it defensively.
	got := findDependencyCycle(map[string]config.ServiceDef{
		"api": svc("api"),
	})
	if got == "" {
		t.Fatal("self-ref must be reported as a cycle")
	}
}

func TestFindDependencyCycle_Transitive(t *testing.T) {
	// a → b → c → a
	got := findDependencyCycle(map[string]config.ServiceDef{
		"a": svc("b"),
		"b": svc("c"),
		"c": svc("a"),
	})
	if got == "" {
		t.Fatal("expected transitive cycle detected")
	}
	for _, n := range []string{"a", "b", "c"} {
		if !strings.Contains(got, n) {
			t.Errorf("cycle description missing %q: %s", n, got)
		}
	}
}

// ── ValidateConfig integration for depends_on ──────────────────────────────

func TestValidateConfig_DependsOn_Valid(t *testing.T) {
	cfg := validCfg()
	cfg.Services = map[string]config.ServiceDef{
		"api":      {Image: "api", DependsOn: []string{"postgres"}},
		"postgres": {Image: "postgres"},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConfig_DependsOn_Unknown(t *testing.T) {
	cfg := validCfg()
	cfg.Services = map[string]config.ServiceDef{
		"api": {Image: "api", DependsOn: []string{"nonexistent"}},
	}
	assertValidationError(t, cfg, "not a defined service")
}

func TestValidateConfig_DependsOn_Self(t *testing.T) {
	cfg := validCfg()
	cfg.Services = map[string]config.ServiceDef{
		"api": {Image: "api", DependsOn: []string{"api"}},
	}
	assertValidationError(t, cfg, "cannot depend on itself")
}

func TestValidateConfig_DependsOn_Cycle(t *testing.T) {
	cfg := validCfg()
	cfg.Services = map[string]config.ServiceDef{
		"a": {Image: "a", DependsOn: []string{"b"}},
		"b": {Image: "b", DependsOn: []string{"a"}},
	}
	assertValidationError(t, cfg, "cycle")
}
