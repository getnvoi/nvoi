package core

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/getnvoi/nvoi/pkg/kube"
)

// testKube returns a *kube.Client backed by client-go fake clientsets,
// pre-populated with objs. Shared across pkg/core tests that need a
// kube client without a real apiserver.
func testKube(objs ...runtime.Object) *kube.Client {
	cs := k8sfake.NewSimpleClientset(objs...)
	dyn := fake.NewSimpleDynamicClient(scheme.Scheme, objs...)
	return kube.NewForTest(cs, dyn)
}
