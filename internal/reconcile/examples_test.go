package reconcile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"

	// Every provider referenced from any examples/*.yaml needs its
	// init() side effect to register with provider.RegisterX. Tests in
	// this package already pull in postgres / neon / build/{local,ssh,
	// daytona} via other _test.go files; the rest land here so the
	// examples test alone covers what the cmd/cli binary covers.
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/planetscale"
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"
)

// TestExamples_ParseAndValidate is the regression gate that keeps
// every operator-facing example in examples/*.yaml from drifting away
// from the live schema. Each file must:
//
//  1. Parse cleanly via config.ParseAppConfig (no unknown-shape errors,
//     no broken UnmarshalYAML hooks).
//  2. Pass ValidateConfig (every cross-field rule the deploy path
//     enforces — engine-specific credentials block requirements,
//     dedicated-node invariant for selfhosted DBs, build-provider
//     capability gate, etc.).
//
// Failure here means an operator copy-pasting an example into nvoi.yaml
// would hit the failure on first deploy. We catch it in CI instead.
func TestExamples_ParseAndValidate(t *testing.T) {
	matches, err := filepath.Glob("../../examples/*.yaml")
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no examples found — examples/ directory empty?")
	}

	for _, path := range matches {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			cfg, err := config.ParseAppConfig(data)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			if err := ValidateConfig(cfg); err != nil {
				t.Fatalf("validate %s: %v", path, err)
			}
		})
	}
}
