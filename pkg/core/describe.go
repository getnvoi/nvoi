package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// kubeGetJSON runs a KubeClient JSON query and unmarshals the result into dest.
func kubeGetJSON(ctx context.Context, kc *kube.KubeClient, ns, resource string, dest any) error {
	out, err := kc.GetJSON(ctx, ns, resource, utils.NvoiSelector)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dest)
}

func kubeGetClusterJSON(ctx context.Context, kc *kube.KubeClient, resource string, dest any) error {
	out, err := kc.GetJSON(ctx, "", resource, "")
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dest)
}

// ── Request / Result types ──────────────────────────────────────────────────────

type DescribeRequest struct {
	Cluster
	Output         Output
	StorageNames   []string            // from cfg — config is the source of truth
	ServiceSecrets map[string][]string // service/cron name → secret keys declared on it
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

type DescribeResult struct {
	Namespace string             `json:"namespace"`
	Nodes     []DescribeNode     `json:"nodes"`
	Workloads []DescribeWorkload `json:"workloads"`
	Pods      []DescribePod      `json:"pods"`
	Services  []DescribeService  `json:"services"`
	Crons     []DescribeCron     `json:"crons"`
	Ingress   []DescribeIngress  `json:"ingress"`
	Secrets   []DescribeSecret   `json:"secrets"`
	Storage   []StorageItem      `json:"storage"`
}

// ── Public ──────────────────────────────────────────────────────────────────────

func Describe(ctx context.Context, req DescribeRequest) (*DescribeResult, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("kube client not available")
	}
	names, err := req.Cluster.Names()
	if err != nil {
		return nil, err
	}

	ns := names.KubeNamespace()
	kc := req.Kube

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

	// Ingress (k8s Ingress resources)
	routes, err := kc.GetIngressRoutes(ctx, ns)
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
			continue // secret may not exist yet
		}
		for _, key := range keys {
			result.Secrets = append(result.Secrets, DescribeSecret{Key: key, Service: svc})
		}
	}

	return result, nil
}

// DescribeJSON returns raw kubectl JSON keyed by resource type.
func DescribeJSON(ctx context.Context, req DescribeRequest) (map[string]json.RawMessage, error) {
	names, err := req.Cluster.Names()
	if err != nil {
		return nil, err
	}

	ns := names.KubeNamespace()
	kc := req.Kube
	sel := utils.NvoiSelector
	result := map[string]json.RawMessage{}

	type query struct {
		key string
		fn  func() ([]byte, error)
	}
	queries := []query{
		{"nodes", func() ([]byte, error) { return kc.GetJSON(ctx, "", "nodes", "") }},
		{"deployments", func() ([]byte, error) { return kc.GetJSON(ctx, ns, "deployments", sel) }},
		{"statefulsets", func() ([]byte, error) { return kc.GetJSON(ctx, ns, "statefulsets", sel) }},
		{"pods", func() ([]byte, error) { return kc.GetJSON(ctx, ns, "pods", sel) }},
		{"services", func() ([]byte, error) { return kc.GetJSON(ctx, ns, "services", sel) }},
		{"cronjobs", func() ([]byte, error) { return kc.GetJSON(ctx, ns, "cronjobs", sel) }},
		// Global "secrets" k8s Secret no longer exists — secrets live in per-service secrets only.
		{"ingresses", func() ([]byte, error) { return kc.GetJSON(ctx, ns, "ingresses", utils.NvoiSelector) }},
	}

	for _, q := range queries {
		if out, err := q.fn(); err == nil && len(out) > 0 {
			result[q.key] = json.RawMessage(out)
		}
	}
	return result, nil
}

// ── kubectl parsers ─────────────────────────────────────────────────────────────

func describeNodes(ctx context.Context, kc *kube.KubeClient) []DescribeNode {
	var resp struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Addresses  []struct{ Type, Address string } `json:"addresses"`
				Conditions []struct{ Type, Status string }  `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if kubeGetClusterJSON(ctx, kc, "nodes", &resp) != nil {
		return nil
	}
	var out []DescribeNode
	for _, n := range resp.Items {
		status := "NotReady"
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				status = "Ready"
			}
		}
		ip := ""
		for _, a := range n.Status.Addresses {
			if a.Type == "InternalIP" {
				ip = a.Address
				break
			}
		}
		out = append(out, DescribeNode{
			Name: n.Metadata.Name, Status: status,
			Role: n.Metadata.Labels[utils.LabelNvoiRole], IP: ip,
		})
	}
	return out
}

func describeWorkloads(ctx context.Context, kc *kube.KubeClient, ns string) []DescribeWorkload {
	var out []DescribeWorkload
	for _, kind := range []string{"deployments", "statefulsets"} {
		var resp kube.WorkloadList
		if kubeGetJSON(ctx, kc, ns, kind, &resp) != nil {
			continue
		}
		kindName := strings.TrimSuffix(kind, "s")
		for _, w := range resp.Items {
			image := ""
			if len(w.Spec.Template.Spec.Containers) > 0 {
				image = w.Spec.Template.Spec.Containers[0].Image
			}
			out = append(out, DescribeWorkload{
				Name: w.Metadata.Name, Kind: kindName,
				Ready: fmt.Sprintf("%d/%d", w.Status.ReadyReplicas, w.Spec.Replicas),
				Image: image, Age: utils.HumanAge(w.Metadata.CreationTimestamp),
			})
		}
	}
	return out
}

func describeCrons(ctx context.Context, kc *kube.KubeClient, ns string) []DescribeCron {
	var resp struct {
		Items []struct {
			Metadata struct {
				Name              string `json:"name"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				Schedule    string `json:"schedule"`
				JobTemplate struct {
					Spec struct {
						Template struct {
							Spec struct {
								Containers []struct {
									Image string `json:"image"`
								} `json:"containers"`
							} `json:"spec"`
						} `json:"template"`
					} `json:"spec"`
				} `json:"jobTemplate"`
			} `json:"spec"`
			Status struct {
				LastScheduleTime *string `json:"lastScheduleTime"`
				Active           []any   `json:"active"`
			} `json:"status"`
		} `json:"items"`
	}
	if kubeGetJSON(ctx, kc, ns, "cronjobs", &resp) != nil {
		return nil
	}
	var out []DescribeCron
	for _, item := range resp.Items {
		status := "idle"
		if len(item.Status.Active) > 0 {
			status = "active"
		} else if item.Status.LastScheduleTime != nil && *item.Status.LastScheduleTime != "" {
			status = "scheduled"
		}
		image := ""
		if len(item.Spec.JobTemplate.Spec.Template.Spec.Containers) > 0 {
			image = item.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image
		}
		out = append(out, DescribeCron{
			Name:     item.Metadata.Name,
			Schedule: item.Spec.Schedule,
			Image:    image,
			Age:      utils.HumanAge(item.Metadata.CreationTimestamp),
			Status:   status,
		})
	}
	return out
}

func describePods(ctx context.Context, kc *kube.KubeClient, ns string) []DescribePod {
	var resp kube.PodList
	if kubeGetJSON(ctx, kc, ns, "pods", &resp) != nil {
		return nil
	}
	var out []DescribePod
	for _, p := range resp.Items {
		status := p.Status.Phase
		restarts := 0
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				status = cs.State.Waiting.Reason
			}
		}
		out = append(out, DescribePod{
			Name: p.Metadata.Name, Status: status,
			Node: p.Spec.NodeName, Restarts: restarts,
			Age: utils.HumanAge(p.Metadata.CreationTimestamp),
		})
	}
	return out
}

func describeServices(ctx context.Context, kc *kube.KubeClient, ns string) []DescribeService {
	var resp struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Spec     struct {
				Type      string `json:"type"`
				ClusterIP string `json:"clusterIP"`
				Ports     []struct {
					Port     int    `json:"port"`
					Protocol string `json:"protocol"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	if kubeGetJSON(ctx, kc, ns, "services", &resp) != nil {
		return nil
	}
	var out []DescribeService
	for _, s := range resp.Items {
		var ports []string
		for _, p := range s.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}
		out = append(out, DescribeService{
			Name: s.Metadata.Name, Type: s.Spec.Type,
			ClusterIP: s.Spec.ClusterIP, Ports: strings.Join(ports, ","),
		})
	}
	return out
}
