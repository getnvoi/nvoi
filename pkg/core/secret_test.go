package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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
		Kube:      kube.NewFromClientset(fake.NewSimpleClientset()),
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
	_ = base64.StdEncoding // keep import used by other tests
	mock := &testutil.MockSSH{}

	ns := "nvoi-myapp-prod"
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "api-secrets", Namespace: ns},
		Data:       map[string][]byte{"JWT_SECRET": []byte("super-secret")},
	})
	c := testCluster(mock)
	c.Kube = kube.NewFromClientset(cs)

	val, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster:      c,
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
	mock := &testutil.MockSSH{}
	ns := "nvoi-myapp-prod"
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "secrets", Namespace: ns},
		Data:       map[string][]byte{"MY_KEY": []byte("legacy-value")},
	})
	c := testCluster(mock)
	c.Kube = kube.NewFromClientset(cs)

	val, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster:      c,
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
	mock := &testutil.MockSSH{}
	c := testCluster(mock)
	c.Kube = kube.NewFromClientset(fake.NewSimpleClientset()) // empty — no secrets

	_, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster:      c,
		Key:          "NOPE",
		ServiceNames: []string{"api"},
	})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}
