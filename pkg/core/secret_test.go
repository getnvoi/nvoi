package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func init() {
	provider.RegisterCompute("test", provider.CredentialSchema{Name: "test"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{
				ID: "1", Name: "nvoi-myapp-prod-master", Status: "running",
				IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
			}},
		}
	})
}

func testCluster(ssh *testutil.MockSSH) Cluster {
	return Cluster{
		AppName: "myapp", Env: "prod",
		Provider: "test", Credentials: map[string]string{},
		Output:    &testutil.MockOutput{},
		MasterSSH: ssh,
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			if ssh == nil {
				return nil, fmt.Errorf("no SSH")
			}
			return ssh, nil
		},
	}
}

func TestSecretList_ConfigDriven(t *testing.T) {
	keys, err := SecretList(context.Background(), SecretListRequest{
		SecretNames: []string{"JWT_SECRET", "ENCRYPTION_KEY"},
	})
	if err != nil {
		t.Fatalf("SecretList: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("SecretList: got %d keys, want 2", len(keys))
	}
	if keys[0] != "JWT_SECRET" || keys[1] != "ENCRYPTION_KEY" {
		t.Errorf("SecretList: keys = %v", keys)
	}
}

func TestSecretList_Empty(t *testing.T) {
	keys, err := SecretList(context.Background(), SecretListRequest{})
	if err != nil {
		t.Fatalf("SecretList: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("SecretList: got %d keys, want 0", len(keys))
	}
}

func TestSecretReveal_FromPerServiceSecret(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("super-secret"))
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// api-secrets has the key
			{Prefix: "get secret api-secrets -o jsonpath='{.data.JWT_SECRET}'", Result: testutil.MockResult{
				Output: []byte("'" + encoded + "'"),
			}},
		},
	}

	val, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster:      testCluster(mock),
		Key:          "JWT_SECRET",
		ServiceNames: []string{"api"},
	})
	if err != nil {
		t.Fatalf("SecretReveal: %v", err)
	}
	if val != "super-secret" {
		t.Errorf("SecretReveal: got %q, want %q", val, "super-secret")
	}
}

func TestSecretReveal_FallbackToGlobalSecret(t *testing.T) {
	// Per-service secret doesn't have it, global does (legacy cluster)
	encoded := base64.StdEncoding.EncodeToString([]byte("legacy-value"))
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret api-secrets", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "get secret secrets -o jsonpath='{.data.MY_KEY}'", Result: testutil.MockResult{
				Output: []byte("'" + encoded + "'"),
			}},
		},
	}

	val, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster:      testCluster(mock),
		Key:          "MY_KEY",
		ServiceNames: []string{"api"},
	})
	if err != nil {
		t.Fatalf("SecretReveal: %v", err)
	}
	if val != "legacy-value" {
		t.Errorf("SecretReveal: got %q, want %q", val, "legacy-value")
	}
}

func TestSecretReveal_NotFound(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
		},
	}

	_, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster:      testCluster(mock),
		Key:          "NOPE",
		ServiceNames: []string{"api"},
	})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}
