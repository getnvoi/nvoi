package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// Kind names a typed resource kind that SweepOwned can list + delete.
// Mirrors the kinds Apply supports — adding a new sweep target is one
// switch case in SweepOwned.
type Kind string

const (
	KindDeployment  Kind = "Deployment"
	KindStatefulSet Kind = "StatefulSet"
	KindService     Kind = "Service"
	KindSecret      Kind = "Secret"
	KindConfigMap   Kind = "ConfigMap"
	KindCronJob     Kind = "CronJob"
	KindPVC         Kind = "PersistentVolumeClaim"
)

// SweepOwned deletes every resource of `kind` in `ns` carrying
// nvoi/owner=<owner> whose name is NOT in `desired`. Pass desired=nil
// (or empty) to sweep ALL resources for that owner+kind — the
// "remove-everything" idiom for migration cleanups (caddy → tunnel,
// tunnel → caddy).
//
// Owner-scoped sweep is the structural fix that obsoletes namespace-
// wide-with-exclusions: the listing itself filters by nvoi/owner, so
// each reconcile step can never see another step's resources. No
// LabelNvoiDatabase exemption, no PurgeTunnelAgents/PurgeCaddy hard-
// coded name lists.
//
// NotFound on Delete is silently ignored — concurrent reconciles or
// manual cleanup are valid no-ops.
func (c *Client) SweepOwned(ctx context.Context, ns, owner string, kind Kind, desired []string) error {
	if owner == "" {
		return fmt.Errorf("SweepOwned: owner required")
	}
	keep := make(map[string]bool, len(desired))
	for _, n := range desired {
		keep[n] = true
	}
	selector := fmt.Sprintf("%s=%s", utils.LabelNvoiOwner, owner)
	listOpts := metav1.ListOptions{LabelSelector: selector}

	switch kind {
	case KindDeployment:
		list, err := c.cs.AppsV1().Deployments(ns).List(ctx, listOpts)
		if err != nil {
			return fmt.Errorf("sweep deployments (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			if keep[item.Name] {
				continue
			}
			if err := IgnoreNotFound(c.cs.AppsV1().Deployments(ns).Delete(ctx, item.Name, metav1.DeleteOptions{})); err != nil {
				return fmt.Errorf("sweep delete deployment/%s: %w", item.Name, err)
			}
		}
	case KindStatefulSet:
		list, err := c.cs.AppsV1().StatefulSets(ns).List(ctx, listOpts)
		if err != nil {
			return fmt.Errorf("sweep statefulsets (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			if keep[item.Name] {
				continue
			}
			if err := IgnoreNotFound(c.cs.AppsV1().StatefulSets(ns).Delete(ctx, item.Name, metav1.DeleteOptions{})); err != nil {
				return fmt.Errorf("sweep delete statefulset/%s: %w", item.Name, err)
			}
		}
	case KindService:
		list, err := c.cs.CoreV1().Services(ns).List(ctx, listOpts)
		if err != nil {
			return fmt.Errorf("sweep services (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			if keep[item.Name] {
				continue
			}
			if err := IgnoreNotFound(c.cs.CoreV1().Services(ns).Delete(ctx, item.Name, metav1.DeleteOptions{})); err != nil {
				return fmt.Errorf("sweep delete service/%s: %w", item.Name, err)
			}
		}
	case KindSecret:
		list, err := c.cs.CoreV1().Secrets(ns).List(ctx, listOpts)
		if err != nil {
			return fmt.Errorf("sweep secrets (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			if keep[item.Name] {
				continue
			}
			if err := IgnoreNotFound(c.cs.CoreV1().Secrets(ns).Delete(ctx, item.Name, metav1.DeleteOptions{})); err != nil {
				return fmt.Errorf("sweep delete secret/%s: %w", item.Name, err)
			}
		}
	case KindConfigMap:
		list, err := c.cs.CoreV1().ConfigMaps(ns).List(ctx, listOpts)
		if err != nil {
			return fmt.Errorf("sweep configmaps (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			if keep[item.Name] {
				continue
			}
			if err := IgnoreNotFound(c.cs.CoreV1().ConfigMaps(ns).Delete(ctx, item.Name, metav1.DeleteOptions{})); err != nil {
				return fmt.Errorf("sweep delete configmap/%s: %w", item.Name, err)
			}
		}
	case KindCronJob:
		list, err := c.cs.BatchV1().CronJobs(ns).List(ctx, listOpts)
		if err != nil {
			return fmt.Errorf("sweep cronjobs (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			if keep[item.Name] {
				continue
			}
			if err := IgnoreNotFound(c.cs.BatchV1().CronJobs(ns).Delete(ctx, item.Name, metav1.DeleteOptions{})); err != nil {
				return fmt.Errorf("sweep delete cronjob/%s: %w", item.Name, err)
			}
		}
	case KindPVC:
		list, err := c.cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, listOpts)
		if err != nil {
			return fmt.Errorf("sweep pvcs (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			if keep[item.Name] {
				continue
			}
			if err := IgnoreNotFound(c.cs.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, item.Name, metav1.DeleteOptions{})); err != nil {
				return fmt.Errorf("sweep delete pvc/%s: %w", item.Name, err)
			}
		}
	default:
		return fmt.Errorf("SweepOwned: unsupported kind %q", kind)
	}
	return nil
}

// ListOwned returns the names of every resource of `kind` in `ns`
// carrying nvoi/owner=<owner>. Read-only mirror of SweepOwned — same
// label-scoping discipline, no deletes. Used by the plan engine to
// diff cfg-vs-live without mutating the cluster.
func (c *Client) ListOwned(ctx context.Context, ns, owner string, kind Kind) ([]string, error) {
	if owner == "" {
		return nil, fmt.Errorf("ListOwned: owner required")
	}
	selector := fmt.Sprintf("%s=%s", utils.LabelNvoiOwner, owner)
	listOpts := metav1.ListOptions{LabelSelector: selector}
	var names []string
	switch kind {
	case KindDeployment:
		list, err := c.cs.AppsV1().Deployments(ns).List(ctx, listOpts)
		if err != nil {
			return nil, fmt.Errorf("list deployments (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
	case KindStatefulSet:
		list, err := c.cs.AppsV1().StatefulSets(ns).List(ctx, listOpts)
		if err != nil {
			return nil, fmt.Errorf("list statefulsets (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
	case KindService:
		list, err := c.cs.CoreV1().Services(ns).List(ctx, listOpts)
		if err != nil {
			return nil, fmt.Errorf("list services (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
	case KindSecret:
		list, err := c.cs.CoreV1().Secrets(ns).List(ctx, listOpts)
		if err != nil {
			return nil, fmt.Errorf("list secrets (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
	case KindConfigMap:
		list, err := c.cs.CoreV1().ConfigMaps(ns).List(ctx, listOpts)
		if err != nil {
			return nil, fmt.Errorf("list configmaps (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
	case KindCronJob:
		list, err := c.cs.BatchV1().CronJobs(ns).List(ctx, listOpts)
		if err != nil {
			return nil, fmt.Errorf("list cronjobs (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
	case KindPVC:
		list, err := c.cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, listOpts)
		if err != nil {
			return nil, fmt.Errorf("list pvcs (owner=%s): %w", owner, err)
		}
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
	default:
		return nil, fmt.Errorf("ListOwned: unsupported kind %q", kind)
	}
	return names, nil
}
