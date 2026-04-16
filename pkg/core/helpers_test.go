package core

import (
	"context"
	"fmt"
	"io"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
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

type silentOutput struct{}

func (silentOutput) Command(_, _, _ string, _ ...any) {}
func (silentOutput) Progress(_ string)                {}
func (silentOutput) Success(_ string)                 {}
func (silentOutput) Warning(_ string)                 {}
func (silentOutput) Info(_ string)                    {}
func (silentOutput) Error(_ error)                    {}
func (silentOutput) Writer() io.Writer                { return io.Discard }
