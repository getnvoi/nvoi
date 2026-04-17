package core

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
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

// testCluster wires a cluster with a pre-built kube fake.
func testCluster(kc *kube.Client) Cluster {
	return Cluster{
		AppName: "myapp", Env: "prod",
		Provider: "test", Credentials: map[string]string{},
		Output:     &testutil.MockOutput{},
		MasterKube: kc,
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return nil, fmt.Errorf("no SSH in tests")
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
		t.Fatalf("got %d keys, want 2", len(keys))
	}
	if keys[0] != "JWT_SECRET" || keys[1] != "ENCRYPTION_KEY" {
		t.Errorf("keys = %v", keys)
	}
}

func TestSecretList_Empty(t *testing.T) {
	keys, err := SecretList(context.Background(), SecretListRequest{})
	if err != nil {
		t.Fatalf("SecretList: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("got %d keys, want 0", len(keys))
	}
}

func TestSecretReveal_FromPerServiceSecret(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "api-secrets", Namespace: "nvoi-myapp-prod"},
		Data:       map[string][]byte{"JWT_SECRET": []byte("super-secret")},
	}
	kc := testKube(existing)

	val, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster:      testCluster(kc),
		Key:          "JWT_SECRET",
		ServiceNames: []string{"api"},
	})
	if err != nil {
		t.Fatalf("SecretReveal: %v", err)
	}
	if val != "super-secret" {
		t.Errorf("got %q, want super-secret", val)
	}
}

func TestSecretReveal_FallbackToGlobalSecret(t *testing.T) {
	// Per-service secret doesn't have MY_KEY, global secrets does (legacy path).
	global := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "secrets", Namespace: "nvoi-myapp-prod"},
		Data:       map[string][]byte{"MY_KEY": []byte("legacy-value")},
	}
	kc := testKube(global)

	val, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster:      testCluster(kc),
		Key:          "MY_KEY",
		ServiceNames: []string{"api"},
	})
	if err != nil {
		t.Fatalf("SecretReveal: %v", err)
	}
	if val != "legacy-value" {
		t.Errorf("got %q, want legacy-value", val)
	}
}

func TestSecretReveal_NotFound(t *testing.T) {
	kc := testKube()
	_, err := SecretReveal(context.Background(), SecretRevealRequest{
		Cluster:      testCluster(kc),
		Key:          "NOPE",
		ServiceNames: []string{"api"},
	})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}
