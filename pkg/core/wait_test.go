package core

import (
	"context"
	"io"
	"io/fs"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func init() {
	provider.RegisterCompute("wait-test", provider.CredentialSchema{Name: "wait-test"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		}
	})
}

func waitCluster(out *testutil.MockOutput, ssh utils.SSHClient) Cluster {
	return Cluster{
		AppName: "test", Env: "prod",
		Provider: "wait-test", Output: out,
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) { return ssh, nil },
	}
}

func TestWaitAllServices_AllReady(t *testing.T) {
	out := &testutil.MockOutput{}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{{
			Prefix: "get pods",
			Result: testutil.MockResult{Output: []byte(`{"items":[
				{"metadata":{"name":"web-abc"},"status":{"phase":"Running","containerStatuses":[{"ready":true,"state":{}}]}},
				{"metadata":{"name":"db-xyz"},"status":{"phase":"Running","containerStatuses":[{"ready":true,"state":{}}]}}
			]}`)},
		}},
	}

	err := WaitAllServices(context.Background(), WaitAllServicesRequest{
		Cluster: waitCluster(out, ssh),
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(out.Successes) == 0 || out.Successes[0] != "all services ready" {
		t.Errorf("expected success 'all services ready', got: %v", out.Successes)
	}
}

func TestWaitAllServices_ReportsNotReady(t *testing.T) {
	call := 0
	out := &testutil.MockOutput{}
	ssh := &countingSSH{
		call:        &call,
		switchAfter: 2,
		before: []byte(`{"items":[
			{"metadata":{"name":"web-abc"},"status":{"phase":"Running","containerStatuses":[{"ready":true,"state":{}}]}},
			{"metadata":{"name":"api-xyz"},"status":{"phase":"Running","containerStatuses":[{"ready":false,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}
		]}`),
		after: []byte(`{"items":[
			{"metadata":{"name":"web-abc"},"status":{"phase":"Running","containerStatuses":[{"ready":true,"state":{}}]}},
			{"metadata":{"name":"api-xyz"},"status":{"phase":"Running","containerStatuses":[{"ready":true,"state":{}}]}}
		]}`),
	}

	err := WaitAllServices(context.Background(), WaitAllServicesRequest{
		Cluster: waitCluster(out, ssh),
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	foundCrashMsg := false
	for _, msg := range out.Progresses {
		if strings.Contains(msg, "api-xyz") && strings.Contains(msg, "CrashLoopBackOff") {
			foundCrashMsg = true
		}
	}
	if !foundCrashMsg {
		t.Errorf("expected progress with 'api-xyz (CrashLoopBackOff)', got: %v", out.Progresses)
	}
}

func TestWaitAllServices_Timeout(t *testing.T) {
	out := &testutil.MockOutput{}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{{
			Prefix: "get pods",
			Result: testutil.MockResult{Output: []byte(`{"items":[
				{"metadata":{"name":"web-abc"},"status":{"phase":"Running","containerStatuses":[{"ready":false,"state":{"waiting":{"reason":"ContainerCreating"}}}]}}
			]}`)},
		}},
	}

	err := WaitAllServices(context.Background(), WaitAllServicesRequest{
		Cluster: waitCluster(out, ssh),
		Timeout: 1 * time.Second,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}

	foundMsg := false
	for _, msg := range out.Progresses {
		if strings.Contains(msg, "web-abc") && strings.Contains(msg, "ContainerCreating") {
			foundMsg = true
		}
	}
	if !foundMsg {
		t.Errorf("expected progress showing stuck pod, got: %v", out.Progresses)
	}
}

// countingSSH switches get-pods response after N calls. Implements utils.SSHClient.
type countingSSH struct {
	call        *int
	switchAfter int
	before      []byte
	after       []byte
}

func (m *countingSSH) Run(_ context.Context, cmd string) ([]byte, error) {
	if strings.Contains(cmd, "get pods") {
		*m.call++
		if *m.call <= m.switchAfter {
			return m.before, nil
		}
		return m.after, nil
	}
	return nil, nil
}

func (m *countingSSH) RunStream(_ context.Context, _ string, _, _ io.Writer) error { return nil }
func (m *countingSSH) Upload(_ context.Context, _ io.Reader, _ string, _ fs.FileMode) error {
	return nil
}
func (m *countingSSH) Stat(_ context.Context, p string) (*utils.RemoteFileInfo, error) {
	return &utils.RemoteFileInfo{Path: p}, nil
}
func (m *countingSSH) DialTCP(_ context.Context, _ string) (net.Conn, error) { return nil, nil }
func (m *countingSSH) Close() error                                          { return nil }

var _ utils.SSHClient = (*countingSSH)(nil)
