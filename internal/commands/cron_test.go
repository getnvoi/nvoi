package commands

import (
	"fmt"
	"testing"
)

func TestCronSet_ParsesFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewCronCmd(m), "set", "cleanup",
		"--image", "busybox", "--schedule", "0 1 * * *", "--command", "echo hi",
	)
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "CronSet")
	assertArg(t, m, 0, "cleanup")
	opts := m.last().Args[1].(CronOpts)
	if opts.Image != "busybox" || opts.Schedule != "0 1 * * *" || opts.Command != "echo hi" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestCronSet_MissingImage(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewCronCmd(m), "set", "cleanup", "--schedule", "0 1 * * *")
	assertError(t, err, "image")
}

func TestCronSet_MissingSchedule(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewCronCmd(m), "set", "cleanup", "--image", "busybox")
	assertError(t, err, "schedule")
}

func TestCronSet_PropagatesError(t *testing.T) {
	m := &MockBackend{Err: fmt.Errorf("backend failure")}
	err := runCmd(t, NewCronCmd(m), "set", "cleanup", "--image", "busybox", "--schedule", "0 * * * *")
	assertError(t, err, "backend failure")
}

func TestCronDelete_ParsesName(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewCronCmd(m), "delete", "cleanup")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "CronDelete")
	assertArg(t, m, 0, "cleanup")
}
