package commands

import "testing"

func TestResources(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewResourcesCmd(m))
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "Resources")
	assertArg(t, m, 0, false) // json default
}
