package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── Request / Result types ──────────────────────────────────────────────────────

type DescribeRequest struct {
	Cluster
	Cfg            provider.ProviderConfigView // forwards to Cluster.Kube for on-demand connect
	StorageNames   []string                    // from cfg — config is the source of truth
	ServiceSecrets map[string][]string         // service/cron name → secret keys declared on it
	// TunnelProvider is non-empty when providers.tunnel is set in nvoi.yaml.
	// When set, Caddy ingress is skipped and tunnel agent pods are queried instead.
	TunnelProvider string
	// TunnelRoutes is the config-derived hostname→service routing table.
	// Pre-built by the caller from cfg.Domains + cfg.Services.
	TunnelRoutes []DescribeIngress
}

type DescribeNode struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Role   string `json:"role"`
	IP     string `json:"ip"`
}

type DescribeWorkload struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`  // "deployment" or "statefulset"
	Ready string `json:"ready"` // "2/2"
	Image string `json:"image"`
	Age   string `json:"age"`
}

type DescribeCron struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Image    string `json:"image"`
	Age      string `json:"age"`
	Status   string `json:"status"`
}

type DescribePod struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Node     string `json:"node"`
	Restarts int    `json:"restarts"`
	Age      string `json:"age"`
}

type DescribeService struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	ClusterIP string `json:"cluster_ip"`
	Ports     string `json:"ports"`
}

type DescribeIngress struct {
	Domain  string `json:"domain"`
	Service string `json:"service"`
	Port    int    `json:"port"`
}

type DescribeSecret struct {
	Key     string `json:"key"`
	Service string `json:"service"` // which service/cron owns this secret
}

// DescribeTunnel holds live state of the active tunnel provider.
// Populated only when providers.tunnel is configured; nil otherwise.
type DescribeTunnel struct {
	Provider string            `json:"provider"`
	Routes   []DescribeIngress `json:"routes"`
	Agents   []DescribePod     `json:"agents"`
}

type DescribeResult struct {
	Namespace string             `json:"namespace"`
	Nodes     []DescribeNode     `json:"nodes"`
	Workloads []DescribeWorkload `json:"workloads"`
	Pods      []DescribePod      `json:"pods"`
	Services  []DescribeService  `json:"services"`
	Crons     []DescribeCron     `json:"crons"`
	Ingress   []DescribeIngress  `json:"ingress"`
	Tunnel    *DescribeTunnel    `json:"tunnel,omitempty"`
	Secrets   []DescribeSecret   `json:"secrets"`
	Storage   []StorageItem      `json:"storage"`
}

// ── Public ──────────────────────────────────────────────────────────────────────

func Describe(ctx context.Context, req DescribeRequest) (*DescribeResult, error) {
	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ns := names.KubeNamespace()

	result := &DescribeResult{Namespace: ns}
	result.Nodes = describeNodes(ctx, kc)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Workloads = describeWorkloads(ctx, kc, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Pods = describePods(ctx, kc, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Services = describeServices(ctx, kc, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Crons = describeCrons(ctx, kc, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}

	if req.TunnelProvider != "" {
		// Tunnel path: Caddy is not running. Read agent pod health from the cluster.
		agents, err := describeTunnelAgents(ctx, kc, ns)
		if err != nil {
			return result, fmt.Errorf("describe tunnel agents: %w", err)
		}
		result.Tunnel = &DescribeTunnel{
			Provider: req.TunnelProvider,
			Routes:   req.TunnelRoutes,
			Agents:   agents,
		}
	} else {
		// Caddy path: read routes from Caddy's live admin API config.
		// Caddy might not be running yet (first deploy in progress) — that's
		// not an error for describe; the routes list just stays empty.
		routes, err := kc.GetCaddyRoutes(ctx)
		if err != nil {
			return result, fmt.Errorf("describe ingress: %w", err)
		}
		for _, r := range routes {
			for _, d := range r.Domains {
				result.Ingress = append(result.Ingress, DescribeIngress{
					Domain: d, Service: r.Service, Port: r.Port,
				})
			}
		}
	}

	// Storage — derived from config, not from scanning k8s secrets
	for _, storageName := range req.StorageNames {
		result.Storage = append(result.Storage, StorageItem{
			Name:   storageName,
			Bucket: names.Bucket(storageName),
		})
	}

	// Secrets — read live keys from each per-service k8s Secret
	for _, svc := range utils.SortedKeys(req.ServiceSecrets) {
		secretName := names.KubeServiceSecrets(svc)
		keys, err := kc.ListSecretKeys(ctx, ns, secretName)
		if err != nil {
			continue
		}
		for _, key := range keys {
			result.Secrets = append(result.Secrets, DescribeSecret{Key: key, Service: svc})
		}
	}

	return result, nil
}

// DescribeJSON returns raw JSON for each kube resource type, preserving the
// shape clients of the legacy command depended on.
func DescribeJSON(ctx context.Context, req DescribeRequest) (map[string]json.RawMessage, error) {
	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ns := names.KubeNamespace()
	sel := kube.NvoiSelector
	result := map[string]json.RawMessage{}

	type query struct {
		key string
		fn  func() (any, error)
	}
	queries := []query{
		{"nodes", func() (any, error) {
			return kc.Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		}},
		{"deployments", func() (any, error) {
			return kc.Clientset().AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"statefulsets", func() (any, error) {
			return kc.Clientset().AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"pods", func() (any, error) {
			return kc.Clientset().CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"services", func() (any, error) {
			return kc.Clientset().CoreV1().Services(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"cronjobs", func() (any, error) {
			return kc.Clientset().BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"ingresses", func() (any, error) {
			return kc.Clientset().NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
	}

	for _, q := range queries {
		obj, err := q.fn()
		if err != nil {
			continue
		}
		if data, err := json.Marshal(obj); err == nil && len(data) > 0 {
			result[q.key] = data
		}
	}
	return result, nil
}

// ── Internal helpers ────────────────────────────────────────────────────────────

func describeNodes(ctx context.Context, kc *kube.Client) []DescribeNode {
	nodes, err := kc.Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	out := make([]DescribeNode, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		status := "NotReady"
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				status = "Ready"
			}
		}
		ip := ""
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				ip = a.Address
				break
			}
		}
		out = append(out, DescribeNode{
			Name:   n.Name,
			Status: status,
			Role:   n.Labels[utils.LabelNvoiRole],
			IP:     ip,
		})
	}
	return out
}

func describeWorkloads(ctx context.Context, kc *kube.Client, ns string) []DescribeWorkload {
	var out []DescribeWorkload

	deps, err := kc.Clientset().AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err == nil {
		for _, d := range deps.Items {
			image := ""
			if len(d.Spec.Template.Spec.Containers) > 0 {
				image = d.Spec.Template.Spec.Containers[0].Image
			}
			replicas := int32(0)
			if d.Spec.Replicas != nil {
				replicas = *d.Spec.Replicas
			}
			out = append(out, DescribeWorkload{
				Name:  d.Name,
				Kind:  "deployment",
				Ready: fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, replicas),
				Image: image,
				Age:   utils.HumanAge(d.CreationTimestamp.Format("2006-01-02T15:04:05Z")),
			})
		}
	}

	ss, err := kc.Clientset().AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err == nil {
		for _, s := range ss.Items {
			image := ""
			if len(s.Spec.Template.Spec.Containers) > 0 {
				image = s.Spec.Template.Spec.Containers[0].Image
			}
			replicas := int32(0)
			if s.Spec.Replicas != nil {
				replicas = *s.Spec.Replicas
			}
			out = append(out, DescribeWorkload{
				Name:  s.Name,
				Kind:  "statefulset",
				Ready: fmt.Sprintf("%d/%d", s.Status.ReadyReplicas, replicas),
				Image: image,
				Age:   utils.HumanAge(s.CreationTimestamp.Format("2006-01-02T15:04:05Z")),
			})
		}
	}
	return out
}

func describeCrons(ctx context.Context, kc *kube.Client, ns string) []DescribeCron {
	list, err := kc.Clientset().BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err != nil {
		return nil
	}
	out := make([]DescribeCron, 0, len(list.Items))
	for _, c := range list.Items {
		status := "idle"
		if len(c.Status.Active) > 0 {
			status = "active"
		} else if c.Status.LastScheduleTime != nil {
			status = "scheduled"
		}
		image := ""
		if len(c.Spec.JobTemplate.Spec.Template.Spec.Containers) > 0 {
			image = c.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image
		}
		out = append(out, DescribeCron{
			Name:     c.Name,
			Schedule: c.Spec.Schedule,
			Image:    image,
			Age:      utils.HumanAge(c.CreationTimestamp.Format("2006-01-02T15:04:05Z")),
			Status:   status,
		})
	}
	return out
}

func describePods(ctx context.Context, kc *kube.Client, ns string) []DescribePod {
	pods, err := kc.Clientset().CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil
		}
		return nil
	}
	out := make([]DescribePod, 0, len(pods.Items))
	for _, p := range pods.Items {
		status := string(p.Status.Phase)
		restarts := 0
		for _, cs := range p.Status.ContainerStatuses {
			restarts += int(cs.RestartCount)
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				status = cs.State.Waiting.Reason
			}
		}
		out = append(out, DescribePod{
			Name:     p.Name,
			Status:   status,
			Node:     p.Spec.NodeName,
			Restarts: restarts,
			Age:      utils.HumanAge(p.CreationTimestamp.Format("2006-01-02T15:04:05Z")),
		})
	}
	return out
}

func describeTunnelAgents(ctx context.Context, kc *kube.Client, ns string) ([]DescribePod, error) {
	rawPods, err := kc.GetTunnelAgentPods(ctx, ns)
	if err != nil {
		return nil, err
	}
	out := make([]DescribePod, 0, len(rawPods))
	for _, p := range rawPods {
		status := string(p.Status.Phase)
		restarts := 0
		for _, cs := range p.Status.ContainerStatuses {
			restarts += int(cs.RestartCount)
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				status = cs.State.Waiting.Reason
			}
		}
		out = append(out, DescribePod{
			Name:     p.Name,
			Status:   status,
			Node:     p.Spec.NodeName,
			Restarts: restarts,
			Age:      utils.HumanAge(p.CreationTimestamp.Format("2006-01-02T15:04:05Z")),
		})
	}
	return out, nil
}

func describeServices(ctx context.Context, kc *kube.Client, ns string) []DescribeService {
	svcs, err := kc.Clientset().CoreV1().Services(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err != nil {
		return nil
	}
	out := make([]DescribeService, 0, len(svcs.Items))
	for _, s := range svcs.Items {
		ports := make([]string, 0, len(s.Spec.Ports))
		for _, p := range s.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}
		out = append(out, DescribeService{
			Name:      s.Name,
			Type:      string(s.Spec.Type),
			ClusterIP: s.Spec.ClusterIP,
			Ports:     strings.Join(ports, ","),
		})
	}
	return out
}
