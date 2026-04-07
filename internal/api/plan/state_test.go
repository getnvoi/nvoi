package plan

import (
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestInfraState_RebuildsDomainsWithoutLegacyProxyState(t *testing.T) {
	orig := describeInfraState
	describeInfraState = func(ctx context.Context, req pkgcore.DescribeRequest) (*pkgcore.DescribeResult, error) {
		return &pkgcore.DescribeResult{
			Workloads: []pkgcore.DescribeWorkload{
				{Name: "web"},
			},
			Ingress: []pkgcore.DescribeIngress{
				{Service: "web", Domain: "example.com"},
				{Service: "web", Domain: "www.example.com"},
			},
		}, nil
	}
	defer func() { describeInfraState = orig }()

	provName := fmt.Sprintf("infra-state-test-%s", t.Name())
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{Name: "nvoi-test-prod-master"}},
		}
	})

	state := InfraState(context.Background(), InfraStateRequest{
		Cluster: pkgcore.Cluster{
			AppName:  "test",
			Env:      "prod",
			Provider: provName,
		},
	})
	if state == nil {
		t.Fatal("expected infra state")
	}
	if len(state.Domains) != 1 {
		t.Fatalf("domains = %v, want one service entry", state.Domains)
	}
	got := state.Domains["web"]
	want := config.Domains{"example.com", "www.example.com"}
	if len(got) != len(want) {
		t.Fatalf("domains[web] = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("domains[web] = %v, want %v", got, want)
		}
	}
}
