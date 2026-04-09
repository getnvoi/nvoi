package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// ── MockBackend ──────────────────────────────────────────────────────────────

// call records a single Backend method invocation.
type call struct {
	Method string
	Args   []any
}

// MockBackend records every Backend call. Set Err to make methods return errors.
type MockBackend struct {
	Calls []call
	Err   error

	// Return values for methods that return data.
	RevealValue string
	LatestValue string
}

func (m *MockBackend) record(method string, args ...any) error {
	m.Calls = append(m.Calls, call{Method: method, Args: args})
	return m.Err
}

func (m *MockBackend) last() call {
	if len(m.Calls) == 0 {
		return call{}
	}
	return m.Calls[len(m.Calls)-1]
}

// ── Backend implementation ──────────────────────────────────────────────────

func (m *MockBackend) InstanceSet(_ context.Context, name, serverType, region, role string) error {
	return m.record("InstanceSet", name, serverType, region, role)
}
func (m *MockBackend) InstanceDelete(_ context.Context, name string) error {
	return m.record("InstanceDelete", name)
}
func (m *MockBackend) InstanceList(_ context.Context) error {
	return m.record("InstanceList")
}
func (m *MockBackend) FirewallSet(_ context.Context, args []string) error {
	return m.record("FirewallSet", args)
}
func (m *MockBackend) FirewallList(_ context.Context) error {
	return m.record("FirewallList")
}
func (m *MockBackend) VolumeSet(_ context.Context, name string, size int, server string) error {
	return m.record("VolumeSet", name, size, server)
}
func (m *MockBackend) VolumeDelete(_ context.Context, name string) error {
	return m.record("VolumeDelete", name)
}
func (m *MockBackend) VolumeList(_ context.Context) error {
	return m.record("VolumeList")
}
func (m *MockBackend) StorageSet(_ context.Context, name, bucket string, cors bool, expireDays int) error {
	return m.record("StorageSet", name, bucket, cors, expireDays)
}
func (m *MockBackend) StorageDelete(_ context.Context, name string) error {
	return m.record("StorageDelete", name)
}
func (m *MockBackend) StorageEmpty(_ context.Context, name string) error {
	return m.record("StorageEmpty", name)
}
func (m *MockBackend) StorageList(_ context.Context) error {
	return m.record("StorageList")
}
func (m *MockBackend) ServiceSet(_ context.Context, name string, opts ServiceOpts) error {
	return m.record("ServiceSet", name, opts)
}
func (m *MockBackend) ServiceDelete(_ context.Context, name string) error {
	return m.record("ServiceDelete", name)
}
func (m *MockBackend) CronSet(_ context.Context, name string, opts CronOpts) error {
	return m.record("CronSet", name, opts)
}
func (m *MockBackend) CronDelete(_ context.Context, name string) error {
	return m.record("CronDelete", name)
}
func (m *MockBackend) DatabaseSet(_ context.Context, name string, opts ManagedOpts) error {
	return m.record("DatabaseSet", name, opts)
}
func (m *MockBackend) DatabaseDelete(_ context.Context, name, kind string) error {
	return m.record("DatabaseDelete", name, kind)
}
func (m *MockBackend) DatabaseList(_ context.Context) error {
	return m.record("DatabaseList")
}
func (m *MockBackend) BackupCreate(_ context.Context, name, kind string) error {
	return m.record("BackupCreate", name, kind)
}
func (m *MockBackend) BackupList(_ context.Context, name, kind, backupStorage string) error {
	return m.record("BackupList", name, kind, backupStorage)
}
func (m *MockBackend) BackupDownload(_ context.Context, name, kind, backupStorage, key string) error {
	return m.record("BackupDownload", name, kind, backupStorage, key)
}
func (m *MockBackend) AgentSet(_ context.Context, name string, opts ManagedOpts) error {
	return m.record("AgentSet", name, opts)
}
func (m *MockBackend) AgentDelete(_ context.Context, name, kind string) error {
	return m.record("AgentDelete", name, kind)
}
func (m *MockBackend) AgentList(_ context.Context) error {
	return m.record("AgentList")
}
func (m *MockBackend) AgentExec(_ context.Context, name, kind string, command []string) error {
	return m.record("AgentExec", name, kind, command)
}
func (m *MockBackend) AgentLogs(_ context.Context, name, kind string, opts LogsOpts) error {
	return m.record("AgentLogs", name, kind, opts)
}
func (m *MockBackend) SecretSet(_ context.Context, key, value string) error {
	return m.record("SecretSet", key, value)
}
func (m *MockBackend) SecretDelete(_ context.Context, key string) error {
	return m.record("SecretDelete", key)
}
func (m *MockBackend) SecretList(_ context.Context) error {
	return m.record("SecretList")
}
func (m *MockBackend) SecretReveal(_ context.Context, key string) (string, error) {
	m.record("SecretReveal", key)
	return m.RevealValue, m.Err
}
func (m *MockBackend) Build(_ context.Context, opts BuildOpts) error {
	return m.record("Build", opts)
}
func (m *MockBackend) BuildList(_ context.Context) error {
	return m.record("BuildList")
}
func (m *MockBackend) BuildLatest(_ context.Context, name string) (string, error) {
	m.record("BuildLatest", name)
	return m.LatestValue, m.Err
}
func (m *MockBackend) BuildPrune(_ context.Context, name string, keep int) error {
	return m.record("BuildPrune", name, keep)
}
func (m *MockBackend) DNSSet(_ context.Context, routes []RouteArg, cf bool) error {
	return m.record("DNSSet", routes, cf)
}
func (m *MockBackend) DNSDelete(_ context.Context, routes []RouteArg) error {
	return m.record("DNSDelete", routes)
}
func (m *MockBackend) DNSList(_ context.Context) error {
	return m.record("DNSList")
}
func (m *MockBackend) IngressSet(_ context.Context, routes []RouteArg, cf bool, cert, key string) error {
	return m.record("IngressSet", routes, cf, cert, key)
}
func (m *MockBackend) IngressDelete(_ context.Context, routes []RouteArg, cf bool) error {
	return m.record("IngressDelete", routes, cf)
}
func (m *MockBackend) Describe(_ context.Context, jsonOutput bool) error {
	return m.record("Describe", jsonOutput)
}
func (m *MockBackend) Resources(_ context.Context, jsonOutput bool) error {
	return m.record("Resources", jsonOutput)
}
func (m *MockBackend) Logs(_ context.Context, service string, opts LogsOpts) error {
	return m.record("Logs", service, opts)
}
func (m *MockBackend) Exec(_ context.Context, service string, command []string) error {
	return m.record("Exec", service, command)
}
func (m *MockBackend) SSH(_ context.Context, command []string) error {
	return m.record("SSH", command)
}

// ── Test helpers ────────────────────────────────────────────────────────────

// runCmd executes a cobra command with the given args. Returns the error.
func runCmd(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	cmd.SetArgs(args)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return cmd.Execute()
}

// assertMethod checks that the last call was to the expected method.
func assertMethod(t *testing.T, m *MockBackend, method string) {
	t.Helper()
	if m.last().Method != method {
		t.Fatalf("expected call to %s, got %s (calls: %v)", method, m.last().Method, m.Calls)
	}
}

// assertArg checks a positional argument from the last call.
func assertArg[T comparable](t *testing.T, m *MockBackend, idx int, want T) {
	t.Helper()
	got, ok := m.last().Args[idx].(T)
	if !ok {
		t.Fatalf("arg[%d] type mismatch: want %T, got %T (%v)", idx, want, m.last().Args[idx], m.last().Args[idx])
	}
	if got != want {
		t.Fatalf("arg[%d] = %v, want %v", idx, got, want)
	}
}

// assertError checks that runCmd returned an error containing substr.
func assertError(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q", substr)
	}
	if substr != "" && !strings.Contains(err.Error(), substr) {
		t.Fatalf("error %q does not contain %q", err.Error(), substr)
	}
}
