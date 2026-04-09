package core

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
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
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return ssh, nil
		},
	}
}

func TestCronSet_MissingImage(t *testing.T) {
	err := CronSet(context.Background(), CronSetRequest{
		Cluster:  testCronCluster(&testutil.MockSSH{}),
		Name:     "backup",
		Schedule: "0 1 * * *",
	})
	if err == nil || !strings.Contains(err.Error(), "--image is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestCronSet_MissingSchedule(t *testing.T) {
	err := CronSet(context.Background(), CronSetRequest{
		Cluster: testCronCluster(&testutil.MockSSH{}),
		Name:    "backup",
		Image:   "busybox",
	})
	if err == nil || !strings.Contains(err.Error(), "--schedule is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestCronSet_InvalidSchedule(t *testing.T) {
	err := CronSet(context.Background(), CronSetRequest{
		Cluster:  testCronCluster(&testutil.MockSSH{}),
		Name:     "backup",
		Image:    "busybox",
		Schedule: "not-a-cron",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid cron schedule") {
		t.Fatalf("err = %v", err)
	}
}

func TestCronSet_MissingSecret(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "get secret secrets -o jsonpath", Result: testutil.MockResult{Output: []byte("'{}'")}},
		},
	}

	err := CronSet(context.Background(), CronSetRequest{
		Cluster:  testCronCluster(mock),
		Name:     "backup",
		Image:    "busybox",
		Schedule: "0 1 * * *",
		Secrets:  []string{"NONEXISTENT"},
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestCronSet_ExpandsStorageRefs(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "get secret secrets -o jsonpath", Result: testutil.MockResult{
				Output: []byte(`'{"STORAGE_BACKUPS_ENDPOINT":"x","STORAGE_BACKUPS_BUCKET":"x","STORAGE_BACKUPS_ACCESS_KEY_ID":"x","STORAGE_BACKUPS_SECRET_ACCESS_KEY":"x"}'`),
			}},
			{Prefix: "replace -f", Result: testutil.MockResult{Output: []byte("not found"), Err: context.DeadlineExceeded}},
			{Prefix: "apply --server-side --force-conflicts -f", Result: testutil.MockResult{}},
		},
	}

	err := CronSet(context.Background(), CronSetRequest{
		Cluster:  testCronCluster(mock),
		Name:     "backup",
		Image:    "busybox",
		Schedule: "0 1 * * *",
		Storages: []string{"backups"},
	})
	if err != nil {
		t.Fatalf("CronSet: %v", err)
	}
	if len(mock.Uploads) == 0 {
		t.Fatal("expected manifest upload")
	}
	content := string(mock.Uploads[0].Content)
	if !strings.Contains(content, "STORAGE_BACKUPS_ENDPOINT") {
		t.Fatalf("manifest missing expanded storage secret refs: %s", content)
	}
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
