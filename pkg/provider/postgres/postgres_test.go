package postgres

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil/kubefake"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestExecSQL_UsesKubeExecAndParsesCSV(t *testing.T) {
	kf := kubefake.NewKubeFake()
	kf.SetExec(func(_ context.Context, req kube.ExecRequest) error {
		if req.Pod != "nvoi-myapp-prod-db-app-0" {
			t.Fatalf("pod = %q", req.Pod)
		}
		if got, want := req.Command[0], "psql"; got != want {
			t.Fatalf("command[0] = %q, want %q", got, want)
		}
		if got, want := req.Command[1], "postgres://appuser:s3cr3t@nvoi-myapp-prod-db-app:5432/myapp?sslmode=disable"; got != want {
			t.Fatalf("dsn = %q, want %q", got, want)
		}
		if _, err := fmt.Fprint(req.Stdout, "n\n1\n"); err != nil {
			t.Fatal(err)
		}
		return nil
	})

	p := &Provider{}
	res, err := p.ExecSQL(context.Background(), provider.DatabaseRequest{
		Name:      "app",
		FullName:  "nvoi-myapp-prod-db-app",
		PodName:   "nvoi-myapp-prod-db-app-0",
		Namespace: "nvoi-myapp-prod",
		Kube:      kf.Client,
		Spec: provider.DatabaseSpec{
			User:     "appuser",
			Password: "s3cr3t",
			Database: "myapp",
		},
	}, "SELECT 1 AS n")
	if err != nil {
		t.Fatalf("ExecSQL: %v", err)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "n" {
		t.Fatalf("columns = %#v", res.Columns)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 || res.Rows[0][0] != "1" {
		t.Fatalf("rows = %#v", res.Rows)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("rows affected = %d", res.RowsAffected)
	}
}

func TestEnsureCredentials_WritesClusterSecret(t *testing.T) {
	kf := kubefake.NewKubeFake()
	p := &Provider{}
	req := provider.DatabaseRequest{
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
		Namespace:             "nvoi-myapp-prod",
		Spec: provider.DatabaseSpec{
			User:     "appuser",
			Password: "s3cr3t",
			Database: "myapp",
		},
	}
	creds, err := p.EnsureCredentials(context.Background(), kf.Client, req)
	if err != nil {
		t.Fatalf("EnsureCredentials: %v", err)
	}
	if creds.URL == "" {
		t.Fatal("expected URL")
	}
	got, err := kf.GetSecretValue(context.Background(), req.Namespace, req.CredentialsSecretName, "url")
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}
	if got != creds.URL {
		t.Fatalf("secret url = %q, want %q", got, creds.URL)
	}
}

func TestParseCSV_Empty(t *testing.T) {
	res, err := parseCSV([]byte{})
	if err != nil {
		t.Fatalf("parseCSV: %v", err)
	}
	if len(res.Columns) != 0 || len(res.Rows) != 0 || res.RowsAffected != 0 {
		t.Fatalf("unexpected result: %#v", res)
	}
}

func TestExecSQL_RequiresKube(t *testing.T) {
	p := &Provider{}
	_, err := p.ExecSQL(context.Background(), provider.DatabaseRequest{}, "SELECT 1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseCSV_MultiColumn(t *testing.T) {
	res, err := parseCSV(bytes.NewBufferString("a,b\n1,2\n").Bytes())
	if err != nil {
		t.Fatalf("parseCSV: %v", err)
	}
	if len(res.Columns) != 2 || res.Columns[1] != "b" {
		t.Fatalf("columns = %#v", res.Columns)
	}
	if len(res.Rows) != 1 || res.Rows[0][1] != "2" {
		t.Fatalf("rows = %#v", res.Rows)
	}
}
