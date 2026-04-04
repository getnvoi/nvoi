package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
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
		Output: &testutil.MockOutput{},
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return ssh, nil
		},
	}
}

func TestSecretSet(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			// get secret returns error = doesn't exist → create path
			{Prefix: "get secret secrets", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "create secret generic", Result: testutil.MockResult{}},
		},
	}

	err := SecretSet(context.Background(), SecretSetRequest{
		Cluster: testCluster(mock),
		Key:     "MY_KEY",
		Value:   "my-value",
	})
	if err != nil {
		t.Fatalf("SecretSet: unexpected error: %v", err)
	}
}

func TestSecretDelete(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// get secret exists (no error)
			{Prefix: "get secret secrets 2>/dev/null", Result: testutil.MockResult{}},
			// ListSecretKeys — returns data with our key
			{Prefix: "get secret secrets -o jsonpath", Result: testutil.MockResult{
				Output: []byte(`'{"MY_KEY":"dGVzdA=="}'`),
			}},
			// patch to remove the key
			{Prefix: "patch secret", Result: testutil.MockResult{}},
		},
	}

	err := SecretDelete(context.Background(), SecretDeleteRequest{
		Cluster: testCluster(mock),
		Key:     "MY_KEY",
	})
	if err != nil {
		t.Fatalf("SecretDelete: unexpected error: %v", err)
	}
}

func TestSecretList(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret secrets -o jsonpath", Result: testutil.MockResult{
				Output: []byte(`'{"KEY1":"dmFsdWUx","KEY2":"dmFsdWUy"}'`),
			}},
		},
	}

	keys, err := SecretList(context.Background(), SecretListRequest{
		Cluster: testCluster(mock),
	})
	if err != nil {
		t.Fatalf("SecretList: unexpected error: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("SecretList: got %d keys, want 2", len(keys))
	}

	sort.Strings(keys)
	if keys[0] != "KEY1" || keys[1] != "KEY2" {
		t.Errorf("SecretList: keys = %v, want [KEY1 KEY2]", keys)
	}
}

func TestSecretReveal(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("super-secret"))
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret secrets -o jsonpath", Result: testutil.MockResult{
				Output: []byte("'" + encoded + "'"),
			}},
		},
	}

	val, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster: testCluster(mock),
		Key:     "MY_KEY",
	})
	if err != nil {
		t.Fatalf("SecretReveal: unexpected error: %v", err)
	}
	if val != "super-secret" {
		t.Errorf("SecretReveal: got %q, want %q", val, "super-secret")
	}
}
