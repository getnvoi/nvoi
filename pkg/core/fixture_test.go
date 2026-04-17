package core

import (
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/nvoi/pkg/kube"
)

// testKube returns a *kube.Client backed by the client-go fake typed
// clientset, pre-populated with objs. Shared across pkg/core tests that
// need a kube client without a real apiserver.
func testKube(objs ...runtime.Object) *kube.Client {
	cs := k8sfake.NewSimpleClientset(objs...)
	return kube.NewForTest(cs)
}
