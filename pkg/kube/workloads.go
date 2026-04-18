package kube

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FirstPod returns the name of the first pod for a service. Errors when no
// pods exist.
func (c *Client) FirstPod(ctx context.Context, ns, service string) (string, error) {
	pods, err := c.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: PodSelector(service),
	})
	if err != nil {
		return "", fmt.Errorf("list pods for %s: %w", service, err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found for service %q", service)
	}
	return pods.Items[0].Name, nil
}

// GetServicePort returns the first port of a Service. Errors if the Service
// has no ports — ingress requires a service with --port.
func (c *Client) GetServicePort(ctx context.Context, ns, name string) (int, error) {
	svc, err := c.cs.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return 0, fmt.Errorf("service %q not found", name)
	}
	if err != nil {
		return 0, fmt.Errorf("get service %s: %w", name, err)
	}
	if len(svc.Spec.Ports) == 0 {
		return 0, fmt.Errorf("service %q has no port", name)
	}
	return int(svc.Spec.Ports[0].Port), nil
}

// ListWorkloadNames returns the deduplicated set of nvoi-managed
// workload names in the namespace (Deployments + StatefulSets,
// label-filtered to the nvoi selector). Used by reconcile.Services for
// orphan detection — names present in kube but absent from cfg get
// ServiceDelete'd.
func (c *Client) ListWorkloadNames(ctx context.Context, ns string) ([]string, error) {
	seen := make(map[string]bool)
	deps, err := c.cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: NvoiSelector})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	for _, d := range deps.Items {
		seen[d.Name] = true
	}
	ss, err := c.cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: NvoiSelector})
	if err != nil {
		return nil, fmt.Errorf("list statefulsets: %w", err)
	}
	for _, s := range ss.Items {
		seen[s.Name] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out, nil
}

// ListCronJobNames returns the nvoi-managed CronJob names in the
// namespace. Mirror of ListWorkloadNames for reconcile.Crons orphan
// detection.
func (c *Client) ListCronJobNames(ctx context.Context, ns string) ([]string, error) {
	cjs, err := c.cs.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{LabelSelector: NvoiSelector})
	if err != nil {
		return nil, fmt.Errorf("list cronjobs: %w", err)
	}
	out := make([]string, 0, len(cjs.Items))
	for _, cj := range cjs.Items {
		out = append(out, cj.Name)
	}
	return out, nil
}

// DeleteByName removes the Deployment, StatefulSet, and Service named `name`.
// Idempotent — NotFound on any of them is treated as already-gone.
func (c *Client) DeleteByName(ctx context.Context, ns, name string) error {
	if err := IgnoreNotFound(c.cs.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete deployment/%s: %w", name, err)
	}
	if err := IgnoreNotFound(c.cs.AppsV1().StatefulSets(ns).Delete(ctx, name, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete statefulset/%s: %w", name, err)
	}
	if err := IgnoreNotFound(c.cs.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete service/%s: %w", name, err)
	}
	return nil
}
