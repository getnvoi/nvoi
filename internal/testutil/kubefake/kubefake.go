// Package kubefake provides a test double for pkg/kube.Client backed by
// the client-go fake typed clientset. Lives outside internal/testutil to
// avoid an import cycle — testutil is imported by pkg/kube's own tests, and
// anything that imports pkg/kube (as this does) can't also be imported from
// tests within pkg/kube.
package kubefake

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/getnvoi/nvoi/pkg/kube"
)

// KubeFake bundles a *kube.Client with a handle to its underlying typed fake
// clientset. Tests can mutate the fake clientset directly to pre-populate
// state, and assert via the typed clientset's tracker.
//
// Pod-level Exec defaults to a "no-op success" responder so reconcilers that
// shell into pods (Caddy admin API, cert wait, HTTPS wait) succeed without
// further wiring. Tests that need to capture stdin or fail Exec can override
// kf.SetExec(fn).
type KubeFake struct {
	*kube.Client
	Typed *k8sfake.Clientset
}

// NewKubeFake returns a KubeFake pre-populated with the given typed objects.
func NewKubeFake(objs ...runtime.Object) *KubeFake {
	cs := k8sfake.NewSimpleClientset(objs...)
	c := kube.NewForTest(cs)
	c.ExecFunc = func(_ context.Context, _ kube.ExecRequest) error { return nil }
	return &KubeFake{
		Client: c,
		Typed:  cs,
	}
}

// SetExec replaces the Exec hook on the underlying *kube.Client. Tests use
// this to capture stdin or return canned errors for cert/HTTPS wait paths.
func (k *KubeFake) SetExec(fn func(ctx context.Context, req kube.ExecRequest) error) {
	k.Client.ExecFunc = fn
}

// SeedReadyPod pre-populates the typed fake with a single Ready Pod for the
// given service in the given namespace. Used by tests that exercise
// WaitRollout — without a Ready pod present, WaitRollout polls until timeout.
func (k *KubeFake) SeedReadyPod(ns, service string) {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      service + "-abc123",
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/name": service},
		},
		Spec: corev1.PodSpec{NodeName: "master"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: service, Ready: true, RestartCount: 0,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
	_, _ = k.Typed.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
}

// AutoReadyPods installs a reactor on the typed fake that responds to any
// "list pods" call with a single ready pod matching the requested label
// selector. Tests that exercise reconcilers calling WaitRollout opt in by
// calling AutoReadyPods() once during setup; without it, list-pods returns
// an empty list and WaitRollout polls forever (or until rolloutTimeout).
//
// Explicit SeedReadyPod calls still take precedence — the reactor is only
// invoked when the tracker holds no pods matching the selector.
func (k *KubeFake) AutoReadyPods() {
	k.Typed.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		la, ok := action.(k8stesting.ListAction)
		if !ok {
			return false, nil, nil
		}
		sel := la.GetListRestrictions().Labels
		if sel == nil || sel.Empty() {
			return false, nil, nil
		}
		// Defer to the tracker if real pods exist.
		listed, err := k.Typed.Tracker().List(
			corev1.SchemeGroupVersion.WithResource("pods"),
			corev1.SchemeGroupVersion.WithKind("PodList"),
			action.GetNamespace(),
		)
		if err == nil {
			if pl, ok := listed.(*corev1.PodList); ok && len(pl.Items) > 0 {
				return false, nil, nil
			}
		}
		// Synthesize a ready pod for the requested service label.
		labels := map[string]string{}
		if reqs, selectable := sel.Requirements(); selectable {
			for _, r := range reqs {
				if r.Key() == "app.kubernetes.io/name" && len(r.Values().List()) > 0 {
					labels[r.Key()] = r.Values().List()[0]
				}
			}
		}
		name := "synth-pod"
		if v, ok := labels["app.kubernetes.io/name"]; ok {
			name = v + "-synth"
		}
		pod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: action.GetNamespace(), Labels: labels},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: name, Ready: true,
					State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				}},
			},
		}
		return true, &corev1.PodList{Items: []corev1.Pod{pod}}, nil
	})
}

// HasResource returns true if the typed clientset's tracker has a resource
// at gvr/namespace/name.
func (k *KubeFake) HasResource(gvr schema.GroupVersionResource, ns, name string) bool {
	_, err := k.Typed.Tracker().Get(gvr, ns, name)
	return err == nil
}

// HasNamespace, HasService, HasDeployment, HasStatefulSet, HasIngress,
// HasCronJob, HasSecret, HasConfigMap, HasPVC are typed shorthands over
// HasResource for the resources nvoi reconciles.
func (k *KubeFake) HasNamespace(name string) bool {
	return k.HasResource(corev1.SchemeGroupVersion.WithResource("namespaces"), "", name)
}
func (k *KubeFake) HasService(ns, name string) bool {
	return k.HasResource(corev1.SchemeGroupVersion.WithResource("services"), ns, name)
}
func (k *KubeFake) HasSecret(ns, name string) bool {
	return k.HasResource(corev1.SchemeGroupVersion.WithResource("secrets"), ns, name)
}
func (k *KubeFake) HasConfigMap(ns, name string) bool {
	return k.HasResource(corev1.SchemeGroupVersion.WithResource("configmaps"), ns, name)
}
func (k *KubeFake) HasPVC(ns, name string) bool {
	return k.HasResource(corev1.SchemeGroupVersion.WithResource("persistentvolumeclaims"), ns, name)
}
func (k *KubeFake) HasDeployment(ns, name string) bool {
	return k.HasResource(appsv1.SchemeGroupVersion.WithResource("deployments"), ns, name)
}
func (k *KubeFake) HasStatefulSet(ns, name string) bool {
	return k.HasResource(appsv1.SchemeGroupVersion.WithResource("statefulsets"), ns, name)
}
func (k *KubeFake) HasIngress(ns, name string) bool {
	return k.HasResource(networkingv1.SchemeGroupVersion.WithResource("ingresses"), ns, name)
}
func (k *KubeFake) HasCronJob(ns, name string) bool {
	return k.HasResource(batchv1.SchemeGroupVersion.WithResource("cronjobs"), ns, name)
}
