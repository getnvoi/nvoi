package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── Request / Result types ──────────────────────────────────────────────────────

type DescribeRequest struct {
	Cluster
	StorageNames []string // from cfg — config is the source of truth
	SecretNames  []string // from cfg — config knows what secrets exist
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
	Key   string `json:"key"`
	Value string `json:"value"` // obfuscated
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
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()

	result := &DescribeResult{Namespace: ns}
	result.Nodes = describeNodes(ctx, ssh)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Workloads = describeWorkloads(ctx, ssh, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Pods = describePods(ctx, ssh, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Services = describeServices(ctx, ssh, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Crons = describeCrons(ctx, ssh, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}

	// Ingress (k8s Ingress resources)
	routes, err := kube.GetIngressRoutes(ctx, ssh, ns)
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

	// Secrets — derived from config, not from scanning k8s secrets
	for _, key := range req.SecretNames {
		result.Secrets = append(result.Secrets, DescribeSecret{Key: key, Value: "(configured)"})
	}

	return result, nil
}

// DescribeJSON returns raw kubectl JSON keyed by resource type.
func DescribeJSON(ctx context.Context, req DescribeRequest) (map[string]json.RawMessage, error) {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	sel := kube.NvoiSelector
	result := map[string]json.RawMessage{}

	type query struct {
		key string
		fn  func() ([]byte, error)
	}
	queries := []query{
		{"nodes", func() ([]byte, error) { return kube.GetClusterJSON(ctx, ssh, "nodes") }},
		{"deployments", func() ([]byte, error) { return kube.GetJSON(ctx, ssh, ns, "deployments", sel) }},
		{"statefulsets", func() ([]byte, error) { return kube.GetJSON(ctx, ssh, ns, "statefulsets", sel) }},
		{"pods", func() ([]byte, error) { return kube.GetJSON(ctx, ssh, ns, "pods", sel) }},
		{"services", func() ([]byte, error) { return kube.GetJSON(ctx, ssh, ns, "services", sel) }},
		{"cronjobs", func() ([]byte, error) { return kube.GetJSON(ctx, ssh, ns, "cronjobs", sel) }},
		// Global "secrets" k8s Secret no longer exists — secrets live in per-service secrets only.
		{"ingresses", func() ([]byte, error) { return kube.GetJSON(ctx, ssh, ns, "ingresses", kube.NvoiSelector) }},
	}

	for _, q := range queries {
		if out, err := q.fn(); err == nil && len(out) > 0 {
			result[q.key] = json.RawMessage(out)
		}
	}
	return result, nil
}

// ── Internal helpers ────────────────────────────────────────────────────────────

// kubeGet runs a kubectl get and unmarshals the JSON result into dest.
func kubeGet(ctx context.Context, ssh utils.SSHClient, ns, resource string, dest any) error {
	out, err := kube.GetJSON(ctx, ssh, ns, resource, kube.NvoiSelector)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dest)
}

func kubeGetCluster(ctx context.Context, ssh utils.SSHClient, resource string, dest any) error {
	out, err := kube.GetClusterJSON(ctx, ssh, resource)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dest)
}

// ── kubectl parsers ─────────────────────────────────────────────────────────────

func describeNodes(ctx context.Context, ssh utils.SSHClient) []DescribeNode {
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
	if kubeGetCluster(ctx, ssh, "nodes", &resp) != nil {
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

func describeWorkloads(ctx context.Context, ssh utils.SSHClient, ns string) []DescribeWorkload {
	var out []DescribeWorkload
	for _, kind := range []string{"deployments", "statefulsets"} {
		var resp kube.WorkloadList
		if kubeGet(ctx, ssh, ns, kind, &resp) != nil {
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

func describeCrons(ctx context.Context, ssh utils.SSHClient, ns string) []DescribeCron {
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
	if kubeGet(ctx, ssh, ns, "cronjobs", &resp) != nil {
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

func describePods(ctx context.Context, ssh utils.SSHClient, ns string) []DescribePod {
	var resp kube.PodList
	if kubeGet(ctx, ssh, ns, "pods", &resp) != nil {
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

func describeServices(ctx context.Context, ssh utils.SSHClient, ns string) []DescribeService {
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
	if kubeGet(ctx, ssh, ns, "services", &resp) != nil {
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
