package commands

import "testing"

func TestAgentSet_ParsesFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewAgentCmd(m), "set", "coder", "--type", "claude", "--secret", "TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "AgentSet")
	assertArg(t, m, 0, "coder")
	opts := m.last().Args[1].(ManagedOpts)
	if opts.Kind != "claude" {
		t.Fatalf("kind = %q", opts.Kind)
	}
}

func TestAgentSet_MissingType(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewAgentCmd(m), "set", "coder")
	assertError(t, err, "Available agent types")
}

func TestAgentDelete_ParsesFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewAgentCmd(m), "delete", "coder", "--type", "claude")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "AgentDelete")
	assertArg(t, m, 0, "coder")
	assertArg(t, m, 1, "claude")
}

func TestAgentList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewAgentCmd(m), "list")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "AgentList")
}

func TestAgentExec(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewAgentCmd(m), "exec", "coder", "--type", "claude", "--", "bash")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "AgentExec")
	assertArg(t, m, 0, "coder")
	assertArg(t, m, 1, "claude")
}

func TestAgentLogs(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewAgentCmd(m), "logs", "coder", "--type", "claude", "-f", "--tail", "100")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "AgentLogs")
	opts := m.last().Args[2].(LogsOpts)
	if !opts.Follow || opts.Tail != 100 {
		t.Fatalf("opts = %+v", opts)
	}
}
