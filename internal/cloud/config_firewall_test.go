package cloud

import (
	"strings"
	"testing"
)

func TestFirewallSet_Default(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Firewall = []string{"default"}
	mustValidate(t, cfg)
}

func TestFirewallSet_Custom(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Firewall = strings.Split("80:0.0.0.0/0,443:0.0.0.0/0", ",")
	mustValidate(t, cfg)
}

func TestFirewallSet_Overwrite(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Firewall = []string{"80:0.0.0.0/0"}
	cfg.Firewall = []string{"default"}

	if cfg.Firewall[0] != "default" {
		t.Fatalf("firewall = %v, want [default]", cfg.Firewall)
	}
	mustValidate(t, cfg)
}
