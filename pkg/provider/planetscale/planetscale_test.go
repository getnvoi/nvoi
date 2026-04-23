package planetscale_test

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/internal/testutil/kubefake"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestEnsureCredentials_WritesSecret(t *testing.T) {
	fake := testutil.NewPlanetScaleFake(t)
	fake.Register("ps-test")
	p, err := provider.ResolveDatabase("ps-test", map[string]string{"service_token": "x", "organization": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	kf := kubefake.NewKubeFake()
	req := provider.DatabaseRequest{
		FullName:              "nvoi-myapp-prod-db-app",
		Namespace:             "nvoi-myapp-prod",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
	}
	creds, err := p.EnsureCredentials(context.Background(), kf.Client, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(creds.URL, "mysql://") {
		t.Fatalf("url = %q", creds.URL)
	}
}

func TestBackupNowAndList(t *testing.T) {
	fake := testutil.NewPlanetScaleFake(t)
	fake.Register("ps-test-backup")
	p, err := provider.ResolveDatabase("ps-test-backup", map[string]string{"service_token": "x", "organization": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	req := provider.DatabaseRequest{FullName: "nvoi-myapp-prod-db-app"}
	if _, err := p.EnsureCredentials(context.Background(), nil, req); err != nil {
		t.Fatal(err)
	}
	ref, err := p.BackupNow(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID == "" {
		t.Fatal("expected backup id")
	}
	refs, err := p.ListBackups(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("expected backup list")
	}
}
