package commands

import "testing"

func TestStorageSet_AllFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewStorageCmd(m), "set", "assets", "--cors", "--expire-days", "30")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "StorageSet")
	assertArg(t, m, 0, "assets")
	assertArg(t, m, 1, "")   // bucket empty
	assertArg(t, m, 2, true) // cors
	assertArg(t, m, 3, 30)   // expire-days
}

func TestStorageSet_Defaults(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewStorageCmd(m), "set", "assets")
	if err != nil {
		t.Fatal(err)
	}
	assertArg(t, m, 2, false) // cors default
	assertArg(t, m, 3, 0)     // expire-days default
}

func TestStorageDelete_ParsesName(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewStorageCmd(m), "delete", "assets")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "StorageDelete")
	assertArg(t, m, 0, "assets")
}

func TestStorageEmpty_ParsesName(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewStorageCmd(m), "empty", "assets")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "StorageEmpty")
	assertArg(t, m, 0, "assets")
}

func TestStorageList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewStorageCmd(m), "list")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "StorageList")
}
