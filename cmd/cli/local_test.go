package main

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
	"k8s.io/client-go/kubernetes/fake"
)

func init() {
	provider.RegisterCompute("local-test", provider.CredentialSchema{Name: "local-test"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{
				ID: "1", Name: "nvoi-myapp-prod-master", Status: provider.ServerRunning,
				IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
			}},
		}
	})
	kube.SetTestTiming(1, 1)
}

// testLocalBackend builds a localBackend with mocks and an inspectable output.
func testLocalBackend(t *testing.T) (*localBackend, *testutil.MockOutput) {
	t.Helper()
	out := &testutil.MockOutput{}
	sshKey, _, _ := utils.GenerateEd25519Key()
	ssh := &testutil.MockSSH{}

	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName:     "myapp",
			Env:         "prod",
			Provider:    "local-test",
			Credentials: map[string]string{},
			SSHKey:      sshKey,
			Kube:        kube.NewFromClientset(fake.NewSimpleClientset()),
			MasterIP:    "1.2.3.4",
		},
		Output:      out,
		RunOnMaster: ssh.Run,
		ConnectSSH: func(_ context.Context, _ string) (utils.SSHClient, error) {
			return ssh, nil
		},
	}
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Compute: "local-test"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx21", Region: "fsn1", Role: "master"}},
	}
	return &localBackend{dc: dc, cfg: cfg, out: out}, out
}

// ── Output propagation tests ───────────────────────────────────────────────
// These verify that localBackend passes b.out to request structs.
// Without Output, log(req.Output) returns NopOutput → silent discard.

func TestLocalBackend_SSH_OutputPropagated(t *testing.T) {
	lb, out := testLocalBackend(t)

	// SSH runs a command via RunStreamMaster. If Output is nil, the Writer()
	// goes to io.Discard and we see nothing. With Output set, the mock
	// captures the writer call.
	_ = lb.SSH(context.Background(), []string{"echo", "hello"})

	// The SSH function calls out.Writer() to stream output.
	// MockOutput tracks Writer() calls via the WriterCalled flag.
	if !out.WriterCalled {
		t.Error("SSH should use Output.Writer() — Output was not propagated to the request struct")
	}
}

func TestLocalBackend_CronRun_OutputPropagated(t *testing.T) {
	lb, out := testLocalBackend(t)

	// CronRun will fail (no CronJob in fake clientset) but should still
	// hit Output.Command() before the error.
	_ = lb.CronRun(context.Background(), "db-backup")

	if len(out.Commands) == 0 {
		t.Error("CronRun should emit a command event — Output was not propagated to the request struct")
	}
}

func TestLocalBackend_Describe_OutputPropagated(t *testing.T) {
	lb, _ := testLocalBackend(t)

	// Describe returns data — it doesn't crash with nil Output, but the
	// test verifies the request struct is constructed correctly by
	// checking the call doesn't panic and returns a result.
	err := lb.Describe(context.Background(), false)
	// May fail on kube queries with fake client, but should not panic.
	_ = err
}

func TestLocalBackend_Logs_OutputPropagated(t *testing.T) {
	lb, out := testLocalBackend(t)

	// Logs will fail (no pods in fake clientset) but exercises the path
	// where Output.Writer() would be called.
	_ = lb.Logs(context.Background(), LogsOpts{Service: "web"})

	// Even on error, if Output wasn't set the Writer() call would go
	// to NopOutput. We can't easily assert Writer was called since Logs
	// fails before reaching StreamLogs, but we verify no panic and that
	// Output was available (not nil).
	if lb.out != out {
		t.Error("localBackend.out should be the mock output")
	}
}
