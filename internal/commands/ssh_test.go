package commands

import "testing"

func TestSSH(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewSSHCmd(m), "uptime")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "SSH")
}
