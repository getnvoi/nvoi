package core

import (
	"io"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/testutil"
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

func testCluster() Cluster {
	return Cluster{
		AppName: "myapp", Env: "prod",
		Provider: "test", Credentials: map[string]string{},
		Kube:     kube.NewFromClientset(fake.NewSimpleClientset()),
		MasterIP: "1.2.3.4",
	}
}

var testOutput Output = &testutil.MockOutput{}

type silentOutput struct{}

func (silentOutput) Command(_, _, _ string, _ ...any) {}
func (silentOutput) Progress(_ string)                {}
func (silentOutput) Success(_ string)                 {}
func (silentOutput) Warning(_ string)                 {}
func (silentOutput) Info(_ string)                    {}
func (silentOutput) Error(_ error)                    {}
func (silentOutput) Writer() io.Writer                { return io.Discard }
