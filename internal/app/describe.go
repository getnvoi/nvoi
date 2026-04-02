package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/kube"
	"github.com/getnvoi/nvoi/internal/provider"
)

// ── Request / Result types ──────────────────────────────────────────────────────

type DescribeRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
}

type DescribeNode struct {
	Name   string
	Status string
	Role   string
	IP     string
}

type DescribeWorkload struct {
	Name  string
	Kind  string // "deployment" or "statefulset"
	Ready string // "2/2"
	Image string
	Age   string
}

type DescribePod struct {
	Name     string
	Status   string
	Node     string
	Restarts int
	Age      string
}

type DescribeService struct {
	Name      string
	Type      string
	ClusterIP string
	Ports     string
}

type DescribeIngress struct {
	Domain  string
	Service string
	Port    int
}

type DescribeSecret struct {
	Key   string
	Value string // obfuscated
}

type DescribeResult struct {
	Namespace string
	Nodes     []DescribeNode
	Workloads []DescribeWorkload
	Pods      []DescribePod
	Services  []DescribeService
	Ingress   []DescribeIngress
	Secrets   []DescribeSecret
	Storage   []StorageItem
}

// ── Public ──────────────────────────────────────────────────────────────────────

func Describe(ctx context.Context, req DescribeRequest) (*DescribeResult, error) {
	ssh, names, err := connectMaster(ctx, req)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()

	result := &DescribeResult{Namespace: ns}
	result.Nodes = describeNodes(ctx, ssh)
	result.Workloads = describeWorkloads(ctx, ssh, ns)
	result.Pods = describePods(ctx, ssh, ns)
	result.Services = describeServices(ctx, ssh, ns)

	// Ingress (Caddy routes)
	routes, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
	for _, r := range routes {
		for _, d := range r.Domains {
			result.Ingress = append(result.Ingress, DescribeIngress{
				Domain: d, Service: r.Service, Port: r.Port,
			})
		}
	}

	// Secrets + Storage (from same k8s Secret)
	keys, err := kube.ListSecretKeys(ctx, ssh, ns, names.KubeSecrets())
	if err == nil {
		secretName := names.KubeSecrets()
		for _, k := range keys {
			if strings.HasPrefix(k, "STORAGE_") {
				if name, ok := parseStorageBucketKey(k); ok {
					if bucket, err := kube.GetSecretValue(ctx, ssh, ns, secretName, k); err == nil {
						result.Storage = append(result.Storage, StorageItem{Name: name, Bucket: bucket})
					}
				}
				continue
			}
			val, _ := kube.GetSecretValue(ctx, ssh, ns, secretName, k)
			result.Secrets = append(result.Secrets, DescribeSecret{Key: k, Value: core.Obfuscate(val)})
		}
	}

	return result, nil
}

// DescribeJSON returns raw kubectl JSON keyed by resource type.
func DescribeJSON(ctx context.Context, req DescribeRequest) (map[string]json.RawMessage, error) {
	ssh, names, err := connectMaster(ctx, req)
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
		{"secrets", func() ([]byte, error) { return kube.GetNamedJSON(ctx, ssh, ns, "secret", names.KubeSecrets()) }},
		{"configmaps", func() ([]byte, error) { return kube.GetNamedJSON(ctx, ssh, ns, "configmap", names.KubeCaddyConfig()) }},
	}

	for _, q := range queries {
		if out, err := q.fn(); err == nil && len(out) > 0 {
			result[q.key] = json.RawMessage(out)
		}
	}
	return result, nil
}

// ── Internal helpers ────────────────────────────────────────────────────────────

func connectMaster(ctx context.Context, req DescribeRequest) (core.SSHClient, *core.Names, error) {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return nil, nil, err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return nil, nil, err
	}
	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return nil, nil, err
	}
	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
	if err != nil {
		return nil, nil, fmt.Errorf("ssh master: %w", err)
	}
	return ssh, names, nil
}

// kubeGet runs a kubectl get and unmarshals the JSON result into dest.
func kubeGet(ctx context.Context, ssh core.SSHClient, ns, resource string, dest any) error {
	out, err := kube.GetJSON(ctx, ssh, ns, resource, kube.NvoiSelector)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dest)
}

func kubeGetCluster(ctx context.Context, ssh core.SSHClient, resource string, dest any) error {
	out, err := kube.GetClusterJSON(ctx, ssh, resource)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dest)
}

// ── kubectl parsers ─────────────────────────────────────────────────────────────

func describeNodes(ctx context.Context, ssh core.SSHClient) []DescribeNode {
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
			Role: n.Metadata.Labels[core.LabelNvoiRole], IP: ip,
		})
	}
	return out
}

// workloadItem is the shared JSON shape for deployments and statefulsets.
type workloadItem struct {
	Metadata struct {
		Name              string `json:"name"`
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int32 `json:"replicas"`
		Template struct {
			Spec struct {
				Containers []struct{ Image string } `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas int32 `json:"readyReplicas"`
	} `json:"status"`
}

func describeWorkloads(ctx context.Context, ssh core.SSHClient, ns string) []DescribeWorkload {
	var out []DescribeWorkload
	for _, kind := range []string{"deployments", "statefulsets"} {
		var resp struct{ Items []workloadItem }
		if kubeGet(ctx, ssh, ns, kind, &resp) != nil {
			continue
		}
		kindName := strings.TrimSuffix(kind, "s")
		for _, w := range resp.Items {
			desired := int32(1)
			if w.Spec.Replicas != nil {
				desired = *w.Spec.Replicas
			}
			image := ""
			if len(w.Spec.Template.Spec.Containers) > 0 {
				image = w.Spec.Template.Spec.Containers[0].Image
			}
			out = append(out, DescribeWorkload{
				Name: w.Metadata.Name, Kind: kindName,
				Ready: fmt.Sprintf("%d/%d", w.Status.ReadyReplicas, desired),
				Image: image, Age: core.HumanAge(w.Metadata.CreationTimestamp),
			})
		}
	}
	return out
}

func describePods(ctx context.Context, ssh core.SSHClient, ns string) []DescribePod {
	var resp struct {
		Items []struct {
			Metadata struct {
				Name              string `json:"name"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec   struct{ NodeName string } `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready        bool `json:"ready"`
					RestartCount int  `json:"restartCount"`
					State        struct {
						Waiting *struct{ Reason string } `json:"waiting"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
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
			Age: core.HumanAge(p.Metadata.CreationTimestamp),
		})
	}
	return out
}

func describeServices(ctx context.Context, ssh core.SSHClient, ns string) []DescribeService {
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

