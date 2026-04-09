package commands

import "testing"

func TestFirewallSet_Preset(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewFirewallCmd(m), "set", "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "FirewallSet")
	args := m.last().Args[0].([]string)
	if len(args) != 1 || args[0] != "cloudflare" {
		t.Fatalf("args = %v", args)
	}
}

func TestFirewallSet_Empty(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewFirewallCmd(m), "set")
	if err != nil {
		t.Fatal(err)
	}
	args := m.last().Args[0].([]string)
	if len(args) != 0 {
		t.Fatalf("args = %v, want empty", args)
	}
}

func TestFirewallList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewFirewallCmd(m), "list")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "FirewallList")
}
