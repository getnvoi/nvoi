package neon_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/internal/testutil/kubefake"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestEnsureCredentials_CreatesProjectAndWritesSecret(t *testing.T) {
	fake := testutil.NewNeonFake(t)
	fake.Register("neon-test")
	p, err := provider.ResolveDatabase("neon-test", map[string]string{"api_key": "x"})
	if err != nil {
		t.Fatal(err)
	}
	kf := kubefake.NewKubeFake()
	req := provider.DatabaseRequest{
		Name:                  "analytics",
		FullName:              "nvoi-myapp-prod-db-analytics",
		Namespace:             "nvoi-myapp-prod",
		CredentialsSecretName: "nvoi-myapp-prod-db-analytics-credentials",
		Spec:                  provider.DatabaseSpec{Region: "eu-central-1"},
	}
	creds, err := p.EnsureCredentials(context.Background(), kf.Client, req)
	if err != nil {
		t.Fatalf("EnsureCredentials: %v", err)
	}
	if creds.Password == "" || creds.Host == "" {
		t.Fatalf("creds = %#v", creds)
	}
	got, err := kf.GetSecretValue(context.Background(), req.Namespace, req.CredentialsSecretName, "password")
	if err != nil {
		t.Fatal(err)
	}
	if got != creds.Password {
		t.Fatalf("secret password = %q, want %q", got, creds.Password)
	}
}

func TestExecSQL_RoundTrip(t *testing.T) {
	fake := testutil.NewNeonFake(t)
	fake.Register("neon-test-sql")
	p, err := provider.ResolveDatabase("neon-test-sql", map[string]string{"api_key": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.EnsureCredentials(context.Background(), nil, provider.DatabaseRequest{FullName: "nvoi-myapp-prod-db-analytics"}); err != nil {
		t.Fatal(err)
	}
	res, err := p.ExecSQL(context.Background(), provider.DatabaseRequest{FullName: "nvoi-myapp-prod-db-analytics"}, "SELECT 1")
	if err != nil {
		t.Fatalf("ExecSQL: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
		t.Fatalf("rows = %#v", res.Rows)
	}
}

func TestDownloadBackup(t *testing.T) {
	fake := testutil.NewNeonFake(t)
	fake.Register("neon-test-backup")
	p, err := provider.ResolveDatabase("neon-test-backup", map[string]string{"api_key": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.EnsureCredentials(context.Background(), nil, provider.DatabaseRequest{FullName: "nvoi-myapp-prod-db-analytics"}); err != nil {
		t.Fatal(err)
	}
	ref, err := p.BackupNow(context.Background(), provider.DatabaseRequest{FullName: "nvoi-myapp-prod-db-analytics"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID == "" {
		t.Fatal("expected ids")
	}
	var buf bytes.Buffer
	if err := p.DownloadBackup(context.Background(), provider.DatabaseRequest{FullName: "nvoi-myapp-prod-db-analytics"}, ref.ID, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), ref.ID) {
		t.Fatalf("dump = %q", buf.String())
	}
}
