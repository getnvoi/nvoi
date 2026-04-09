package commands

import "testing"

func TestLogs_Default(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewLogsCmd(m), "web")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "Logs")
	assertArg(t, m, 0, "web")
	opts := m.last().Args[1].(LogsOpts)
	if opts.Tail != 50 {
		t.Fatalf("tail = %d, want 50 (default)", opts.Tail)
	}
}

func TestLogs_AllFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewLogsCmd(m), "web", "-f", "-n", "100", "--since", "5m", "--previous", "--timestamps")
	if err != nil {
		t.Fatal(err)
	}
	opts := m.last().Args[1].(LogsOpts)
	if !opts.Follow || opts.Tail != 100 || opts.Since != "5m" || !opts.Previous || !opts.Timestamps {
		t.Fatalf("opts = %+v", opts)
	}
}
