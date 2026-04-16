package core

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	"k8s.io/client-go/kubernetes/fake"
)

func init() {
	provider.RegisterCompute("cron-test", provider.CredentialSchema{Name: "cron-test"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{
				ID: "1", Name: "nvoi-myapp-prod-master", Status: "running",
				IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
			}},
			Volumes: []*provider.Volume{{
				Name: "nvoi-myapp-prod-pgdata",
			}},
		}
	})
}

func testCronCluster(ssh *testutil.MockSSH) Cluster {
	return Cluster{
		AppName: "myapp", Env: "prod",
		Provider: "cron-test", Credentials: map[string]string{},
		Output: &testutil.MockOutput{},
		Kube:   kube.NewFromClientset(fake.NewSimpleClientset()),
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return ssh, nil
		},
	}
}

func TestCronSet_SvcSecretsInManifest(t *testing.T) {
	mock := &testutil.MockSSH{}

	err := CronSet(context.Background(), CronSetRequest{
		Cluster:    testCronCluster(mock),
		Name:       "backup",
		Image:      "busybox",
		Schedule:   "0 1 * * *",
		SvcSecrets: []string{"STORAGE_BACKUPS_ENDPOINT", "STORAGE_BACKUPS_BUCKET"},
	})
	if err != nil {
		t.Fatalf("CronSet: %v", err)
	}
	// YAML generation correctness is tested in pkg/kube/cron_test.go.
	// This integration test verifies CronSet succeeds end-to-end with secrets.
}

func TestCronSet_ResolvesNamedManagedVolumes(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "replace -f", Result: testutil.MockResult{Output: []byte("not found"), Err: context.DeadlineExceeded}},
			{Prefix: "apply --server-side --force-conflicts -f", Result: testutil.MockResult{}},
		},
	}

	err := CronSet(context.Background(), CronSetRequest{
		Cluster:  testCronCluster(mock),
		Name:     "backup",
		Image:    "busybox",
		Schedule: "0 1 * * *",
		Volumes:  []string{"pgdata:/data"},
	})
	if err != nil {
		t.Fatalf("CronSet: %v", err)
	}
	if len(mock.Uploads) == 0 {
		t.Fatal("expected manifest upload")
	}
	if !strings.Contains(string(mock.Uploads[0].Content), "/mnt/data/nvoi-myapp-prod-pgdata") {
		t.Fatalf("manifest should use managed volume mount path, got: %s", string(mock.Uploads[0].Content))
	}
}

func TestCronRun_Success(t *testing.T) {
	succeededJob := `{"status":{"succeeded":1,"failed":0}}`
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create job", Result: testutil.MockResult{}},
			{Prefix: "get job", Result: testutil.MockResult{Output: []byte(succeededJob)}},
			{Prefix: "logs", Result: testutil.MockResult{Output: []byte("backup complete\n")}},
		},
	}

	c := testCronCluster(mock)
	c.MasterSSH = mock

	err := CronRun(context.Background(), CronRunRequest{Cluster: c, Name: "db-backup"})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	found := false
	for _, call := range mock.Calls {
		if strings.Contains(call, "create job") && strings.Contains(call, "--from=cronjob/db-backup") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected create job --from=cronjob/db-backup, calls: %v", mock.Calls)
	}
}

func TestCronRun_JobFailed(t *testing.T) {
	failedJob := `{"status":{"succeeded":0,"failed":1}}`
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create job", Result: testutil.MockResult{}},
			{Prefix: "get job", Result: testutil.MockResult{Output: []byte(failedJob)}},
			{Prefix: "logs", Result: testutil.MockResult{Output: []byte("pg_dump: connection refused\n")}},
		},
	}

	c := testCronCluster(mock)
	c.MasterSSH = mock

	err := CronRun(context.Background(), CronRunRequest{Cluster: c, Name: "db-backup"})
	if err == nil {
		t.Fatal("expected error for failed job")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("error should mention failure, got: %v", err)
	}
}

func TestCronDelete_IdempotentWhenMissing(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "delete cronjob/backup --ignore-not-found", Result: testutil.MockResult{}},
		},
	}

	err := CronDelete(context.Background(), CronDeleteRequest{
		Cluster: testCronCluster(mock),
		Name:    "backup",
	})
	if err != nil {
		t.Fatalf("CronDelete: %v", err)
	}
}
