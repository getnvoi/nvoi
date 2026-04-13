package cloud

import "testing"

func TestProviderSet_Compute(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Compute = "aws"
	mustValidate(t, cfg)
}

func TestProviderSet_MissingCompute(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Compute = ""
	mustFailValidation(t, cfg, "providers.compute is required")
}

func TestProviderSet_DNS(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.DNS = "cloudflare"
	mustValidate(t, cfg)
}

func TestProviderSet_Storage(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Storage = "aws"
	mustValidate(t, cfg)
}

func TestProviderSet_Build(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Build = "local"
	mustValidate(t, cfg)
}

func TestProviderSet_Overwrite(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Compute = "hetzner"
	cfg.Providers.Compute = "scaleway"
	if cfg.Providers.Compute != "scaleway" {
		t.Fatalf("compute = %q, want scaleway", cfg.Providers.Compute)
	}
	mustValidate(t, cfg)
}
