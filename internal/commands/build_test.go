package commands

import "testing"

func TestBuild_SingleTarget(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewBuildCmd(m), "--target", "web:./src")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "Build")
	opts := m.last().Args[0].(BuildOpts)
	if len(opts.Targets) != 1 || opts.Targets[0] != "web:./src" {
		t.Fatalf("targets = %v", opts.Targets)
	}
}

func TestBuild_MultiTarget(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewBuildCmd(m), "--target", "web:./src", "--target", "api:./api")
	if err != nil {
		t.Fatal(err)
	}
	opts := m.last().Args[0].(BuildOpts)
	if len(opts.Targets) != 2 {
		t.Fatalf("targets = %v", opts.Targets)
	}
}

func TestBuild_MissingTarget(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewBuildCmd(m))
	assertError(t, err, "target")
}

func TestBuildLatest(t *testing.T) {
	m := &MockBackend{LatestValue: "10.0.1.1:5000/web:20260401"}
	err := runCmd(t, NewBuildCmd(m), "latest", "web")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "BuildLatest")
	assertArg(t, m, 0, "web")
}

func TestBuildPrune(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewBuildCmd(m), "prune", "web", "--keep", "3")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "BuildPrune")
	assertArg(t, m, 0, "web")
	assertArg(t, m, 1, 3)
}

func TestBuildList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewBuildCmd(m), "list")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "BuildList")
}
