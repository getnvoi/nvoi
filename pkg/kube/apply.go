// Package kube wraps client-go with nvoi's deployment conventions:
// server-side apply with the "nvoi" field manager, idempotent secret
// upserts, watch-driven rollout monitoring, namespace+label scoping.
//
// Every operation goes through *Client, which holds a typed clientset and
// a dynamic clientset over an SSH-tunneled connection to the apiserver.
package kube

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// FieldManager identifies nvoi as the owner of fields it sets via
// server-side apply. Conflicting field manipulation surfaces as a real error
// (not silently overwritten) unless --force-conflicts is set on the patch.
const FieldManager = "nvoi"

// NvoiSelector is the label selector matching every nvoi-managed resource.
var NvoiSelector = fmt.Sprintf("%s=%s", utils.LabelAppManagedBy, utils.LabelManagedBy)

// PodSelector returns the label selector matching pods of a given service.
func PodSelector(service string) string {
	return fmt.Sprintf("%s=%s", utils.LabelAppName, service)
}

// gvrFor maps every GVK we handle to its REST resource. Static — no
// discovery roundtrip, no fake-discovery-mapper gymnastics in tests.
// Add entries as new resource kinds get used.
var gvrFor = map[schema.GroupVersionKind]schema.GroupVersionResource{
	corev1.SchemeGroupVersion.WithKind("Namespace"):                   corev1.SchemeGroupVersion.WithResource("namespaces"),
	corev1.SchemeGroupVersion.WithKind("Service"):                     corev1.SchemeGroupVersion.WithResource("services"),
	corev1.SchemeGroupVersion.WithKind("Secret"):                      corev1.SchemeGroupVersion.WithResource("secrets"),
	corev1.SchemeGroupVersion.WithKind("ConfigMap"):                   corev1.SchemeGroupVersion.WithResource("configmaps"),
	corev1.SchemeGroupVersion.WithKind("Pod"):                         corev1.SchemeGroupVersion.WithResource("pods"),
	corev1.SchemeGroupVersion.WithKind("Node"):                        corev1.SchemeGroupVersion.WithResource("nodes"),
	{Group: "apps", Version: "v1", Kind: "Deployment"}:                {Group: "apps", Version: "v1", Resource: "deployments"},
	{Group: "apps", Version: "v1", Kind: "StatefulSet"}:               {Group: "apps", Version: "v1", Resource: "statefulsets"},
	{Group: "batch", Version: "v1", Kind: "Job"}:                      {Group: "batch", Version: "v1", Resource: "jobs"},
	{Group: "batch", Version: "v1", Kind: "CronJob"}:                  {Group: "batch", Version: "v1", Resource: "cronjobs"},
	{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"}:      {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	{Group: "helm.cattle.io", Version: "v1", Kind: "HelmChartConfig"}: {Group: "helm.cattle.io", Version: "v1", Resource: "helmchartconfigs"},
}

// gvkOf resolves a typed object's GroupVersionKind. Falls back to the scheme
// registry if the object's TypeMeta is empty (which is common with hand-built
// typed objects).
func gvkOf(obj runtime.Object) (schema.GroupVersionKind, error) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		gvk := u.GroupVersionKind()
		if gvk.Kind == "" {
			return schema.GroupVersionKind{}, fmt.Errorf("unstructured object missing GVK")
		}
		return gvk, nil
	}
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind != "" {
		return gvk, nil
	}
	gvks, _, err := scheme.Scheme.ObjectKinds(obj)
	if err != nil {
		return schema.GroupVersionKind{}, fmt.Errorf("resolve GVK: %w", err)
	}
	if len(gvks) == 0 {
		return schema.GroupVersionKind{}, fmt.Errorf("no GVK registered for %T", obj)
	}
	return gvks[0], nil
}

// resourceFor returns the dynamic ResourceInterface for an object, plus a
// boolean indicating whether the resource is namespace-scoped. The object's
// GVK must be in gvrFor.
func (c *Client) resourceFor(obj runtime.Object, ns string) (dynamic.ResourceInterface, schema.GroupVersionKind, error) {
	gvk, err := gvkOf(obj)
	if err != nil {
		return nil, schema.GroupVersionKind{}, err
	}
	gvr, ok := gvrFor[gvk]
	if !ok {
		return nil, gvk, fmt.Errorf("unknown GVK %s — add to gvrFor in pkg/kube/apply.go", gvk)
	}
	if isClusterScoped(gvk) {
		return c.dyn.Resource(gvr), gvk, nil
	}
	if ns == "" {
		// Try to read namespace from the object itself.
		if accessor, ok := obj.(metav1.Object); ok {
			ns = accessor.GetNamespace()
		}
	}
	if ns == "" {
		return nil, gvk, fmt.Errorf("%s requires a namespace", gvk.Kind)
	}
	return c.dyn.Resource(gvr).Namespace(ns), gvk, nil
}

func isClusterScoped(gvk schema.GroupVersionKind) bool {
	switch gvk.Kind {
	case "Namespace", "Node":
		return true
	}
	return false
}

// Apply upserts a typed object as nvoi.
//
// For built-in core/apps/batch/networking kinds the typed clientset is
// used directly (Get → Create-if-missing → Update-otherwise). This gives
// idempotent rolling updates and works against the standard fake clientset
// without server-side-apply emulation gymnastics.
//
// For unknown kinds (e.g. HelmChartConfig CRDs) the dynamic client is used
// with server-side-apply Patch.
func (c *Client) Apply(ctx context.Context, ns string, obj runtime.Object) error {
	gvk, err := gvkOf(obj)
	if err != nil {
		return err
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)

	if accessor, ok := obj.(metav1.Object); ok && !isClusterScoped(gvk) {
		if accessor.GetNamespace() == "" && ns != "" {
			accessor.SetNamespace(ns)
		}
	}

	name := ""
	if accessor, ok := obj.(metav1.Object); ok {
		name = accessor.GetName()
	}
	if name == "" {
		return fmt.Errorf("%s missing metadata.name", gvk.Kind)
	}

	// Typed dispatch — preferred path for every kind we ship by default.
	if handled, err := c.applyTyped(ctx, ns, gvk, name, obj); handled {
		return err
	}

	// Dynamic SSA fallback — used for HelmChartConfig and any future CRDs.
	return c.applyDynamic(ctx, ns, gvk, name, obj)
}

// applyTyped dispatches to the typed clientset for known resource kinds.
// Returns handled=true (with err==nil on success) when the GVK is recognized.
func (c *Client) applyTyped(ctx context.Context, ns string, gvk schema.GroupVersionKind, name string, obj runtime.Object) (bool, error) {
	switch typed := obj.(type) {
	case *corev1.Namespace:
		_, err := c.cs.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.CoreV1().Namespaces().Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		_, err = c.cs.CoreV1().Namespaces().Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	case *corev1.Service:
		existing, err := c.cs.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.CoreV1().Services(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		// Preserve immutable fields (ClusterIP, ResourceVersion).
		typed.Spec.ClusterIP = existing.Spec.ClusterIP
		typed.ResourceVersion = existing.ResourceVersion
		_, err = c.cs.CoreV1().Services(ns).Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	case *corev1.Secret:
		_, err := c.cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.CoreV1().Secrets(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		_, err = c.cs.CoreV1().Secrets(ns).Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	case *corev1.ConfigMap:
		_, err := c.cs.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.CoreV1().ConfigMaps(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		_, err = c.cs.CoreV1().ConfigMaps(ns).Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	case *appsv1.Deployment:
		_, err := c.cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.AppsV1().Deployments(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		_, err = c.cs.AppsV1().Deployments(ns).Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	case *appsv1.StatefulSet:
		_, err := c.cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.AppsV1().StatefulSets(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		_, err = c.cs.AppsV1().StatefulSets(ns).Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	case *batchv1.CronJob:
		_, err := c.cs.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.BatchV1().CronJobs(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		_, err = c.cs.BatchV1().CronJobs(ns).Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	case *batchv1.Job:
		_, err := c.cs.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.BatchV1().Jobs(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		_, err = c.cs.BatchV1().Jobs(ns).Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	case *networkingv1.Ingress:
		_, err := c.cs.NetworkingV1().Ingresses(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.NetworkingV1().Ingresses(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		_, err = c.cs.NetworkingV1().Ingresses(ns).Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	}
	return false, nil
}

// applyDynamic SSA-patches via the dynamic client. Used for CRDs (eg
// HelmChartConfig). Falls back to Create on NotFound for parity with real
// apiserver upsert semantics.
func (c *Client) applyDynamic(ctx context.Context, ns string, gvk schema.GroupVersionKind, name string, obj runtime.Object) error {
	ri, _, err := c.resourceFor(obj, ns)
	if err != nil {
		return err
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", gvk.Kind, err)
	}
	force := true
	_, err = ri.Patch(ctx, name, types.ApplyPatchType, data, metav1.PatchOptions{
		FieldManager: FieldManager,
		Force:        &force,
	})
	if err == nil {
		return nil
	}
	if apierrors.IsNotFound(err) {
		u, cerr := toUnstructured(obj)
		if cerr != nil {
			return fmt.Errorf("apply %s/%s: convert to unstructured: %w", gvk.Kind, name, cerr)
		}
		if _, cerr := ri.Create(ctx, u, metav1.CreateOptions{FieldManager: FieldManager}); cerr != nil {
			return fmt.Errorf("apply %s/%s: %w", gvk.Kind, name, cerr)
		}
		return nil
	}
	return fmt.Errorf("apply %s/%s: %w", gvk.Kind, name, err)
}

func wrapApply(gvk schema.GroupVersionKind, name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("apply %s/%s: %w", gvk.Kind, name, err)
}

// toUnstructured converts any typed runtime.Object to *unstructured.Unstructured.
func toUnstructured(obj runtime.Object) (*unstructured.Unstructured, error) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u, nil
	}
	out := &unstructured.Unstructured{}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	out.Object = m
	return out, nil
}

// EnsureNamespace creates a Namespace if missing. Idempotent.
func (c *Client) EnsureNamespace(ctx context.Context, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	return c.Apply(ctx, "", ns)
}

// IgnoreNotFound returns nil when err is a NotFound API error, err otherwise.
// Lets callers write `return IgnoreNotFound(client.Delete(...))` without
// re-implementing the check.
func IgnoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
