package commands

import "testing"

func TestExec(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewExecCmd(m), "web", "--", "bash", "-l")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "Exec")
	assertArg(t, m, 0, "web")
}

func TestExec_MissingCommand(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewExecCmd(m), "web")
	assertError(t, err, "")
}
