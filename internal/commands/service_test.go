package commands

import (
	"fmt"
	"testing"
)

func TestServiceSet_FullFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewServiceCmd(m), "set", "web",
		"--image", "nginx", "--port", "80", "--replicas", "2",
		"--command", "serve", "--server", "worker-1", "--health-path", "/up",
		"--env", "RAILS_ENV=production", "--secret", "KEY", "--storage", "assets",
		"--volume", "data:/var/data", "--no-wait",
	)
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "ServiceSet")
	assertArg(t, m, 0, "web")
	opts := m.last().Args[1].(ServiceOpts)
	if opts.Image != "nginx" || opts.Port != 80 || opts.Replicas != 2 {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.Command != "serve" || opts.Server != "worker-1" || opts.Health != "/up" {
		t.Fatalf("opts = %+v", opts)
	}
	if !opts.NoWait {
		t.Fatal("expected NoWait=true")
	}
}

func TestServiceSet_MinimalFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewServiceCmd(m), "set", "web", "--image", "nginx")
	if err != nil {
		t.Fatal(err)
	}
	opts := m.last().Args[1].(ServiceOpts)
	if opts.Image != "nginx" {
		t.Fatalf("image = %q", opts.Image)
	}
	if opts.Replicas != 1 {
		t.Fatalf("replicas = %d, want 1 (default)", opts.Replicas)
	}
}

func TestServiceSet_SecretRejectsAlias(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewServiceCmd(m), "set", "web", "--image", "nginx", "--secret", "KEY=VALUE")
	assertError(t, err, "use the secret key name directly")
}

func TestServiceSet_RequiresImage(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewServiceCmd(m), "set", "web")
	assertError(t, err, "image")
}

func TestServiceSet_PropagatesError(t *testing.T) {
	m := &MockBackend{Err: fmt.Errorf("backend failure")}
	err := runCmd(t, NewServiceCmd(m), "set", "web", "--image", "nginx")
	assertError(t, err, "backend failure")
}

func TestServiceDelete_ParsesName(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewServiceCmd(m), "delete", "web")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "ServiceDelete")
	assertArg(t, m, 0, "web")
}

func TestServiceDelete_PropagatesError(t *testing.T) {
	m := &MockBackend{Err: fmt.Errorf("backend failure")}
	err := runCmd(t, NewServiceCmd(m), "delete", "web")
	assertError(t, err, "backend failure")
}
