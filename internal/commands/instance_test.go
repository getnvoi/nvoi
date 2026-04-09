package commands

import (
	"fmt"
	"testing"
)

func TestInstanceSet_Master(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewInstanceCmd(m), "set", "master", "--compute-type", "cx23", "--compute-region", "fsn1", "--role", "master")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "InstanceSet")
	assertArg(t, m, 0, "master")
	assertArg(t, m, 1, "cx23")
	assertArg(t, m, 2, "fsn1")
	assertArg(t, m, 3, "master")
}

func TestInstanceSet_Worker(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewInstanceCmd(m), "set", "worker-1", "--compute-type", "cx33", "--compute-region", "fsn1", "--role", "worker")
	if err != nil {
		t.Fatal(err)
	}
	assertArg(t, m, 0, "worker-1")
	assertArg(t, m, 3, "worker")
}

func TestInstanceSet_MissingRole(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewInstanceCmd(m), "set", "master", "--compute-type", "cx23", "--compute-region", "fsn1")
	assertError(t, err, "role")
}

func TestInstanceSet_MissingName(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewInstanceCmd(m), "set", "--compute-type", "cx23", "--compute-region", "fsn1", "--role", "master")
	assertError(t, err, "")
}

func TestInstanceDelete_ParsesName(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewInstanceCmd(m), "delete", "master")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "InstanceDelete")
	assertArg(t, m, 0, "master")
}

func TestInstanceDelete_MissingName(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewInstanceCmd(m), "delete")
	assertError(t, err, "")
}

func TestInstanceList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewInstanceCmd(m), "list")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "InstanceList")
}

func TestInstanceSet_PropagatesError(t *testing.T) {
	m := &MockBackend{Err: fmt.Errorf("backend failure")}
	err := runCmd(t, NewInstanceCmd(m), "set", "master", "--compute-type", "cx23", "--compute-region", "fsn1", "--role", "master")
	assertError(t, err, "backend failure")
}
