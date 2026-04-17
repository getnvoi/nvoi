// Package kube wraps client-go with nvoi's deployment conventions:
// idempotent typed apply with the "nvoi" field manager, secret upserts,
// watch-driven rollout monitoring, namespace+label scoping.
//
// Every operation goes through *Client, which holds a typed clientset over
// an SSH-tunneled connection to the apiserver.
package kube

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// FieldManager identifies nvoi as the owner of fields it sets via the typed
// clientset. Conflicting field manipulation by another manager surfaces as a
// real error on the next apply rather than silent overwrite.
const FieldManager = "nvoi"

// NvoiSelector is the label selector matching every nvoi-managed resource.
var NvoiSelector = fmt.Sprintf("%s=%s", utils.LabelAppManagedBy, utils.LabelManagedBy)

// PodSelector returns the label selector matching pods of a given service.
func PodSelector(service string) string {
	return fmt.Sprintf("%s=%s", utils.LabelAppName, service)
}

// gvkOf resolves a typed object's GroupVersionKind. Falls back to the scheme
// registry if the object's TypeMeta is empty (which is common with hand-built
// typed objects).
func gvkOf(obj runtime.Object) (schema.GroupVersionKind, error) {
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

// Apply upserts a typed object as nvoi.
//
// Every kind we ship is dispatched through the typed clientset
// (Get → Create-if-missing → Update-otherwise) with FieldManager: "nvoi".
// This gives idempotent rolling updates and works against the standard fake
// clientset without server-side-apply emulation gymnastics.
//
// Unknown kinds error out — there is no dynamic / SSA fallback. Add the kind
// to applyTyped if you need a new resource type.
func (c *Client) Apply(ctx context.Context, ns string, obj runtime.Object) error {
	gvk, err := gvkOf(obj)
	if err != nil {
		return err
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)

	if accessor, ok := obj.(metav1.Object); ok && gvk.Kind != "Namespace" {
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

	handled, err := c.applyTyped(ctx, ns, gvk, name, obj)
	if handled {
		return err
	}
	return fmt.Errorf("apply %s/%s: unsupported kind %s — add it to applyTyped in pkg/kube/apply.go", gvk.Kind, name, gvk)
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
	case *corev1.PersistentVolumeClaim:
		existing, err := c.cs.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.CoreV1().PersistentVolumeClaims(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		// PVC spec is largely immutable post-create; we treat existence as
		// success and leave it alone. Re-running Apply on a PVC must not error.
		_ = existing
		return true, nil
	case *appsv1.Deployment:
		existing, err := c.cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.AppsV1().Deployments(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		// Real apiserver: Update ignores .status (status has its own
		// subresource). The client-go fake doesn't model that, so mirror
		// the behavior explicitly — otherwise re-applying a Ready
		// Deployment in tests resets ReadyReplicas to 0.
		typed.Status = existing.Status
		typed.ResourceVersion = existing.ResourceVersion
		_, err = c.cs.AppsV1().Deployments(ns).Update(ctx, typed, metav1.UpdateOptions{FieldManager: FieldManager})
		return true, wrapApply(gvk, name, err)
	case *appsv1.StatefulSet:
		existing, err := c.cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.cs.AppsV1().StatefulSets(ns).Create(ctx, typed, metav1.CreateOptions{FieldManager: FieldManager})
			return true, wrapApply(gvk, name, err)
		}
		if err != nil {
			return true, wrapApply(gvk, name, err)
		}
		typed.Status = existing.Status
		typed.ResourceVersion = existing.ResourceVersion
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

func wrapApply(gvk schema.GroupVersionKind, name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("apply %s/%s: %w", gvk.Kind, name, err)
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
