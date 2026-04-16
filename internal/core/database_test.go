package core

import (
	"encoding/base64"
	"fmt"
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

// b64 encodes a string to base64, matching kubectl's secret output format.
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// kctlPrefix builds the command prefix that kube.kctl() generates.
func kctlPrefix(ns string) string {
	return fmt.Sprintf("KUBECONFIG=/home/%s/.kube/config kubectl -n %s ", utils.DefaultUser, ns)
}

// newDBTestContext builds a DeployContext with MockSSH for database tests.
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
	t.Skip("TODO: ExecInPod uses SPDY — needs real cluster or httptest SPDY server")
	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})
	ssh.Prefixes = []testutil.MockPrefix{
		{
			Prefix: kctlPrefix(ns) + "exec main-db-0 -- psql",
			Result: testutil.MockResult{Output: []byte(" count\n-------\n     1\n(1 row)\n")},
		},
	}

	dbSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "main-db-credentials", Namespace: ns},
		Data: map[string][]byte{
			"MAIN_POSTGRES_USER": []byte("pguser"),
			"MAIN_POSTGRES_DB":   []byte("mydb"),
		},
	}

	dc := newDBTestContext(ssh, dbSecret)
	err := DatabaseSQL(newCmd(), dc, "main", "postgres", "SELECT count(*) FROM users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the psql command was constructed correctly.
	var found bool
	for _, call := range ssh.Calls {
		if strings.Contains(call, "psql -U pguser -d mydb") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected psql command with user/db, got calls: %v", ssh.Calls)
	}
}

func TestDatabaseSQL_MySQL(t *testing.T) {
	t.Skip("TODO: ExecInPod uses SPDY — needs real cluster or httptest SPDY server")
	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})
	ssh.Prefixes = []testutil.MockPrefix{
		{
			Prefix: kctlPrefix(ns) + "exec main-db-0 -- mysql",
			Result: testutil.MockResult{Output: []byte("1 row in set")},
		},
	}

	dbSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "main-db-credentials", Namespace: ns},
		Data: map[string][]byte{
			"MAIN_MYSQL_USER":     []byte("root"),
			"MAIN_MYSQL_DATABASE": []byte("appdb"),
		},
	}

	dc := newDBTestContext(ssh, dbSecret)
	err := DatabaseSQL(newCmd(), dc, "main", "mysql", "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must hit mysql directly — no psql attempt.
	for _, call := range ssh.Calls {
		if strings.Contains(call, "psql") {
			t.Fatalf("should not attempt psql for mysql engine, got call: %s", call)
		}
	}
	var found bool
	for _, call := range ssh.Calls {
		if strings.Contains(call, "mysql -u root appdb") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected mysql command with user/db, got calls: %v", ssh.Calls)
	}
}

func TestDatabaseSQL_NoCreds(t *testing.T) {
	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})
	ssh.Prefixes = []testutil.MockPrefix{
		{
			Prefix: kctlPrefix(ns) + "get secret",
			Result: testutil.MockResult{Err: fmt.Errorf("not found")},
		},
	}

	dc := newDBTestContext(ssh)
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
	// Start a minimal S3-compatible test server that returns XML list response.
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
	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})
	ssh.Prefixes = []testutil.MockPrefix{
		{
			Prefix: kctlPrefix(ns) + "get secret",
			Result: testutil.MockResult{Err: fmt.Errorf("not found")},
		},
	}

	dc := newDBTestContext(ssh)
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
	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})
	ssh.Prefixes = []testutil.MockPrefix{
		{
			Prefix: kctlPrefix(ns) + "get secret",
			Result: testutil.MockResult{Err: fmt.Errorf("not found")},
		},
	}

	dc := newDBTestContext(ssh)
	err := DatabaseBackupDownload(newCmd(), dc, "main", "backup.sql.gz", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "backup bucket credentials not found") {
		t.Fatalf("error = %q, want bucket credentials error", err.Error())
	}
}

func TestDatabaseBackupDownload_Success(t *testing.T) {
	// S3 server that serves a backup file.
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
	outFile := t.TempDir() + "/downloaded.sql.gz"
	err := DatabaseBackupDownload(newCmd(), dc, "main", "backups/2025-01-01.sql.gz", outFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
