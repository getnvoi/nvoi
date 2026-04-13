package core

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
)

// ── Config parsed once via PersistentPreRunE ─────────────────────────────────

func TestNewDatabaseCmd_ResolvesDBNameFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/nvoi.yaml"
	os.WriteFile(cfgPath, []byte("app: myapp\nenv: prod\nproviders:\n  compute: hetzner\nservers:\n  master:\n    type: cx21\n    region: fsn1\n    role: master\ndatabase:\n  analytics:\n    image: postgres:17\n    volume: pgdata\n"), 0o644)

	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})
	ssh.Prefixes = []testutil.MockPrefix{
		// Respond to secret lookups for "analytics" db (analytics-db-credentials)
		{
			Prefix: kctlPrefix(ns) + "get secret analytics-db-credentials",
			Result: testutil.MockResult{Err: fmt.Errorf("not found")},
		},
	}

	dc := newDBTestContext(ssh)
	dbCmd := NewDatabaseCmd(dc)

	root := &cobra.Command{Use: "nvoi", SilenceUsage: true}
	root.PersistentFlags().String("config", cfgPath, "")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error { return nil }
	root.AddCommand(dbCmd)

	root.SetArgs([]string{"db", "sql", "SELECT 1", "--config", cfgPath})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	// Resolved to "analytics" (from config) → tried analytics-db-credentials.
	// If it fell back to "main", it would try main-db-credentials instead.
	if !strings.Contains(err.Error(), `"analytics"`) {
		t.Fatalf("expected resolve to 'analytics' from config, got: %v", err)
	}
}

func TestNewDatabaseCmd_FlagOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/nvoi.yaml"
	os.WriteFile(cfgPath, []byte("app: myapp\nenv: prod\nproviders:\n  compute: hetzner\nservers:\n  master:\n    type: cx21\n    region: fsn1\n    role: master\ndatabase:\n  analytics:\n    image: postgres:17\n    volume: pgdata\n"), 0o644)

	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})
	ssh.Prefixes = []testutil.MockPrefix{
		{
			Prefix: kctlPrefix(ns) + "get secret custom-db-credentials",
			Result: testutil.MockResult{Err: fmt.Errorf("not found")},
		},
	}

	dc := newDBTestContext(ssh)
	dbCmd := NewDatabaseCmd(dc)

	root := &cobra.Command{Use: "nvoi", SilenceUsage: true}
	root.PersistentFlags().String("config", cfgPath, "")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error { return nil }
	root.AddCommand(dbCmd)

	root.SetArgs([]string{"db", "sql", "SELECT 1", "--config", cfgPath, "--name", "custom"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	// --name=custom overrides config's "analytics" → tried custom-db-credentials.
	if !strings.Contains(err.Error(), `"custom"`) {
		t.Fatalf("--name flag didn't override config, got: %v", err)
	}
}

// b64 encodes a string to base64, matching kubectl's secret output format.
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// kctlPrefix builds the command prefix that kube.kctl() generates.
func kctlPrefix(ns string) string {
	return fmt.Sprintf("KUBECONFIG=/home/%s/.kube/config kubectl -n %s ", utils.DefaultUser, ns)
}

// newDBTestContext builds a DeployContext with MockSSH for database tests.
func newDBTestContext(ssh *testutil.MockSSH) *config.DeployContext {
	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName:   "myapp",
			Env:       "prod",
			MasterSSH: ssh,
			Output:    silentOutput{},
		},
	}
}

func newCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("config", "/nonexistent", "")
	return cmd
}

// ── DatabaseSQL ─────────────────────────────────────────────────────────────

func TestDatabaseSQL_Postgres(t *testing.T) {
	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})
	ssh.Prefixes = []testutil.MockPrefix{
		{
			Prefix: kctlPrefix(ns) + "get secret main-db-credentials -o jsonpath='{.data.MAIN_POSTGRES_USER}'",
			Result: testutil.MockResult{Output: []byte(b64("pguser"))},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret main-db-credentials -o jsonpath='{.data.MAIN_POSTGRES_DB}'",
			Result: testutil.MockResult{Output: []byte(b64("mydb"))},
		},
		{
			Prefix: kctlPrefix(ns) + "exec main-db-0 -- psql",
			Result: testutil.MockResult{Output: []byte(" count\n-------\n     1\n(1 row)\n")},
		},
	}

	dc := newDBTestContext(ssh)
	err := DatabaseSQL(newCmd(), dc, "main", "SELECT count(*) FROM users")
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
	ns := "nvoi-myapp-prod"
	ssh := testutil.NewMockSSH(map[string]testutil.MockResult{})
	ssh.Prefixes = []testutil.MockPrefix{
		{
			// Postgres creds not found — empty responses.
			Prefix: kctlPrefix(ns) + "get secret main-db-credentials -o jsonpath='{.data.MAIN_POSTGRES_USER}'",
			Result: testutil.MockResult{Err: fmt.Errorf("not found")},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret main-db-credentials -o jsonpath='{.data.MAIN_POSTGRES_DB}'",
			Result: testutil.MockResult{Err: fmt.Errorf("not found")},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret main-db-credentials -o jsonpath='{.data.MAIN_MYSQL_USER}'",
			Result: testutil.MockResult{Output: []byte(b64("root"))},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret main-db-credentials -o jsonpath='{.data.MAIN_MYSQL_DATABASE}'",
			Result: testutil.MockResult{Output: []byte(b64("appdb"))},
		},
		{
			// psql fails (not postgres)
			Prefix: kctlPrefix(ns) + "exec main-db-0 -- psql",
			Result: testutil.MockResult{Err: fmt.Errorf("psql not found")},
		},
		{
			Prefix: kctlPrefix(ns) + "exec main-db-0 -- mysql",
			Result: testutil.MockResult{Output: []byte("1 row in set")},
		},
	}

	dc := newDBTestContext(ssh)
	err := DatabaseSQL(newCmd(), dc, "main", "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
	err := DatabaseSQL(newCmd(), dc, "main", "SELECT 1")
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
	ssh.Prefixes = []testutil.MockPrefix{
		{
			Prefix: kctlPrefix(ns) + "get secret secrets -o jsonpath='{.data.STORAGE_MAIN_DB_BACKUPS_ENDPOINT}'",
			Result: testutil.MockResult{Output: []byte(b64(s3.URL))},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret secrets -o jsonpath='{.data.STORAGE_MAIN_DB_BACKUPS_BUCKET}'",
			Result: testutil.MockResult{Output: []byte(b64("test-bucket"))},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret secrets -o jsonpath='{.data.STORAGE_MAIN_DB_BACKUPS_ACCESS_KEY_ID}'",
			Result: testutil.MockResult{Output: []byte(b64("AKID"))},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret secrets -o jsonpath='{.data.STORAGE_MAIN_DB_BACKUPS_SECRET_ACCESS_KEY}'",
			Result: testutil.MockResult{Output: []byte(b64("secret"))},
		},
	}

	dc := newDBTestContext(ssh)
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
	ssh.Prefixes = []testutil.MockPrefix{
		{
			Prefix: kctlPrefix(ns) + "get secret secrets -o jsonpath='{.data.STORAGE_MAIN_DB_BACKUPS_ENDPOINT}'",
			Result: testutil.MockResult{Output: []byte(b64(s3.URL))},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret secrets -o jsonpath='{.data.STORAGE_MAIN_DB_BACKUPS_BUCKET}'",
			Result: testutil.MockResult{Output: []byte(b64("test-bucket"))},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret secrets -o jsonpath='{.data.STORAGE_MAIN_DB_BACKUPS_ACCESS_KEY_ID}'",
			Result: testutil.MockResult{Output: []byte(b64("AKID"))},
		},
		{
			Prefix: kctlPrefix(ns) + "get secret secrets -o jsonpath='{.data.STORAGE_MAIN_DB_BACKUPS_SECRET_ACCESS_KEY}'",
			Result: testutil.MockResult{Output: []byte(b64("secret"))},
		},
	}

	dc := newDBTestContext(ssh)
	outFile := t.TempDir() + "/downloaded.sql.gz"
	err := DatabaseBackupDownload(newCmd(), dc, "main", "backups/2025-01-01.sql.gz", outFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
