package agent

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/render"
	"github.com/getnvoi/nvoi/pkg/kube"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBuildDeployContext_KubeOnCluster(t *testing.T) {
	cfg := &config.AppConfig{App: "test", Env: "prod"}
	kc := kube.NewFromClientset(fake.NewSimpleClientset())
	opts := AgentOpts{Kube: kc}

	dc := BuildDeployContext(context.Background(), render.NewJSONOutput(nil), cfg, opts)

	if dc.Cluster.Kube == nil {
		t.Fatal("Cluster.Kube is nil — every pkg/core/ function will panic")
	}
	if dc.Cluster.Kube != kc {
		t.Fatal("Cluster.Kube should be the same KubeClient passed via AgentOpts")
	}
}
