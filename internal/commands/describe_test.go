package commands

import "testing"

func TestDescribe(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDescribeCmd(m))
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "Describe")
	assertArg(t, m, 0, false) // json default
}
