package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func kctlPrefix(ns string) string {
	return fmt.Sprintf("KUBECONFIG=/home/%s/.kube/config kubectl -n %s ", utils.DefaultUser, ns)
}

func newDBTestContext(ssh *testutil.MockSSH, objects ...runtime.Object) *config.DeployContext {
	cs := fake.NewSimpleClientset(objects...)
	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName:   "myapp",
			Env:       "prod",
			MasterSSH: ssh,
			Kube:      kube.NewFromClientset(cs),
			Output:    silentOutput{},
		},
	}
}

func newCmd() *cobra.Command {
	return &cobra.Command{}
}

// ── DatabaseSQL ─────────────────────────────────────────────────────────────

func TestDatabaseSQL_Postgres(t *testing.T) {
	ns := "nvoi-myapp-prod"

	dbSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "main-db-credentials", Namespace: ns},
		Data: map[string][]byte{
			"MAIN_POSTGRES_USER": []byte("pguser"),
			"MAIN_POSTGRES_DB":   []byte("mydb"),
		},
	}

	var gotCommand []string
	cs := fake.NewSimpleClientset(dbSecret)
	kc := kube.NewFromClientset(cs)
	kc.ExecHook = func(_ context.Context, _, _ string, command []string, stdout, _ io.Writer) error {
		gotCommand = command
		fmt.Fprint(stdout, " count\n-------\n     1\n(1 row)\n")
		return nil
	}

	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Kube:   kc,
			Output: silentOutput{},
		},
	}

	err := DatabaseSQL(newCmd(), dc, "main", "postgres", "SELECT count(*) FROM users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify command was constructed correctly.
	if len(gotCommand) == 0 {
		t.Fatal("ExecHook not called")
	}
	cmdStr := strings.Join(gotCommand, " ")
	if !strings.Contains(cmdStr, "psql") || !strings.Contains(cmdStr, "pguser") || !strings.Contains(cmdStr, "mydb") {
		t.Errorf("command = %v, want psql with pguser/mydb", gotCommand)
	}
}

func TestDatabaseSQL_MySQL(t *testing.T) {
	ns := "nvoi-myapp-prod"

	dbSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "main-db-credentials", Namespace: ns},
		Data: map[string][]byte{
			"MAIN_MYSQL_USER":     []byte("root"),
			"MAIN_MYSQL_DATABASE": []byte("appdb"),
		},
	}

	var gotCommand []string
	cs := fake.NewSimpleClientset(dbSecret)
	kc := kube.NewFromClientset(cs)
	kc.ExecHook = func(_ context.Context, _, _ string, command []string, stdout, _ io.Writer) error {
		gotCommand = command
		fmt.Fprint(stdout, "1 row in set")
		return nil
	}

	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Kube:   kc,
			Output: silentOutput{},
		},
	}

	err := DatabaseSQL(newCmd(), dc, "main", "mysql", "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cmdStr := strings.Join(gotCommand, " ")
	if strings.Contains(cmdStr, "psql") {
		t.Fatalf("should not attempt psql for mysql engine, got: %v", gotCommand)
	}
	if !strings.Contains(cmdStr, "mysql") || !strings.Contains(cmdStr, "root") || !strings.Contains(cmdStr, "appdb") {
		t.Errorf("command = %v, want mysql with root/appdb", gotCommand)
	}
}

func TestDatabaseSQL_NoCreds(t *testing.T) {
	cs := fake.NewSimpleClientset() // no secrets
	kc := kube.NewFromClientset(cs)

	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Kube:   kc,
			Output: silentOutput{},
		},
	}

	err := DatabaseSQL(newCmd(), dc, "main", "postgres", "SELECT 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "credentials not found") {
		t.Fatalf("error = %q, want credentials not found", err.Error())
	}
}

// ── DatabaseBackupList ──────────────────────────────────────────────────────

func TestDatabaseBackupList_Success(t *testing.T) {
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <Contents>
    <Key>backups/2025-01-01.sql.gz</Key>
    <Size>1024</Size>
    <LastModified>2025-01-01T00:00:00Z</LastModified>
  </Contents>
</ListBucketResult>`)
	}))
	defer s3.Close()

	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})

	backupSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "main-db-backup-secrets", Namespace: ns},
		Data: map[string][]byte{
			"STORAGE_MAIN_DB_BACKUPS_ENDPOINT":          []byte(s3.URL),
			"STORAGE_MAIN_DB_BACKUPS_BUCKET":            []byte("test-bucket"),
			"STORAGE_MAIN_DB_BACKUPS_ACCESS_KEY_ID":     []byte("AKID"),
			"STORAGE_MAIN_DB_BACKUPS_SECRET_ACCESS_KEY": []byte("secret"),
		},
	}

	dc := newDBTestContext(ssh, backupSecret)
	err := DatabaseBackupList(newCmd(), dc, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDatabaseBackupList_NoBucketCreds(t *testing.T) {
	dc := newDBTestContext(testutil.NewMockSSH(map[string]testutil.MockResult{}))
	err := DatabaseBackupList(newCmd(), dc, "main")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "backup bucket credentials not found") {
		t.Fatalf("error = %q, want bucket credentials error", err.Error())
	}
}

// ── DatabaseBackupDownload ──────────────────────────────────────────────────

func TestDatabaseBackupDownload_NoBucketCreds(t *testing.T) {
	dc := newDBTestContext(testutil.NewMockSSH(map[string]testutil.MockResult{}))
	err := DatabaseBackupDownload(newCmd(), dc, "main", "backup.sql.gz", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "backup bucket credentials not found") {
		t.Fatalf("error = %q, want bucket credentials error", err.Error())
	}
}

func TestDatabaseBackupDownload_Success(t *testing.T) {
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "11")
		fmt.Fprint(w, "backup data")
	}))
	defer s3.Close()

	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})

	backupSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "main-db-backup-secrets", Namespace: ns},
		Data: map[string][]byte{
			"STORAGE_MAIN_DB_BACKUPS_ENDPOINT":          []byte(s3.URL),
			"STORAGE_MAIN_DB_BACKUPS_BUCKET":            []byte("test-bucket"),
			"STORAGE_MAIN_DB_BACKUPS_ACCESS_KEY_ID":     []byte("AKID"),
			"STORAGE_MAIN_DB_BACKUPS_SECRET_ACCESS_KEY": []byte("secret"),
		},
	}

	dc := newDBTestContext(ssh, backupSecret)
	err := DatabaseBackupDownload(newCmd(), dc, "main", "backup.sql.gz", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
