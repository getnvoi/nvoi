package commands

import "testing"

func TestVolumeSet_ParsesFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewVolumeCmd(m), "set", "pgdata", "--size", "30", "--server", "master")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "VolumeSet")
	assertArg(t, m, 0, "pgdata")
	assertArg(t, m, 1, 30)
	assertArg(t, m, 2, "master")
}

func TestVolumeDelete_ParsesName(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewVolumeCmd(m), "delete", "pgdata")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "VolumeDelete")
	assertArg(t, m, 0, "pgdata")
}

func TestVolumeList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewVolumeCmd(m), "list")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "VolumeList")
}
