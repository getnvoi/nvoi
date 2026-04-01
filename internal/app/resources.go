package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/provider"
)

type ResourcesRequest struct {
	Provider    string
	Credentials map[string]string
	DNSProvider string
	DNSCreds    map[string]string
	SSHKey      []byte
}

type K8sNode struct {
	Name   string
	Status string
	Roles  string
}

type K8sPod struct {
	Namespace string
	Name      string
	Status    string
	Node      string
}

type K8sService struct {
	Namespace string
	Name      string
	Type      string
	ClusterIP string
	Ports     string
}

type ResourcesResult struct {
	Servers    []*provider.Server
	Firewalls  []*provider.Firewall
	Networks   []*provider.Network
	DNSRecords []provider.DNSRecord
	K8sNodes   []K8sNode
	K8sPods    []K8sPod
	K8sServices []K8sService
}

func Resources(ctx context.Context, req ResourcesRequest) (*ResourcesResult, error) {
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return nil, err
	}

	servers, err := prov.ListAllServers(ctx)
	if err != nil {
		return nil, err
	}
	firewalls, err := prov.ListAllFirewalls(ctx)
	if err != nil {
		return nil, err
	}
	networks, err := prov.ListAllNetworks(ctx)
	if err != nil {
		return nil, err
	}

	result := &ResourcesResult{
		Servers:   servers,
		Firewalls: firewalls,
		Networks:  networks,
	}

	// DNS records (optional)
	if req.DNSProvider != "" {
		dns, err := provider.ResolveDNS(req.DNSProvider, req.DNSCreds)
		if err == nil {
			records, err := dns.ListARecords(ctx)
			if err == nil {
				result.DNSRecords = records
			}
		}
	}

	// K8s resources — find master, SSH in, query kubectl
	if len(req.SSHKey) > 0 {
		for _, s := range servers {
			if strings.Contains(s.Name, "-master") && s.Status == "running" {
				ssh, err := infra.ConnectSSH(ctx, s.IPv4+":22", core.DefaultUser, req.SSHKey)
				if err != nil {
					break
				}
				defer ssh.Close()

				kubeconfig := fmt.Sprintf("KUBECONFIG=/home/%s/.kube/config", core.DefaultUser)

				// Nodes
				result.K8sNodes = fetchNodes(ctx, ssh, kubeconfig)

				// Pods (all namespaces, nvoi-managed only)
				result.K8sPods = fetchPods(ctx, ssh, kubeconfig)

				// Services (all namespaces, nvoi-managed only)
				result.K8sServices = fetchServices(ctx, ssh, kubeconfig)

				break
			}
		}
	}

	return result, nil
}

func fetchNodes(ctx context.Context, ssh core.SSHClient, kubeconfig string) []K8sNode {
	out, err := ssh.Run(ctx, fmt.Sprintf("%s kubectl get nodes -o json", kubeconfig))
	if err != nil {
		return nil
	}
	var resp struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(out, &resp) != nil {
		return nil
	}

	var nodes []K8sNode
	for _, item := range resp.Items {
		status := "NotReady"
		for _, c := range item.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				status = "Ready"
			}
		}
		roles := item.Metadata.Labels[core.LabelNvoiRole]
		nodes = append(nodes, K8sNode{Name: item.Metadata.Name, Status: status, Roles: roles})
	}
	return nodes
}

func fetchPods(ctx context.Context, ssh core.SSHClient, kubeconfig string) []K8sPod {
	out, err := ssh.Run(ctx, fmt.Sprintf("%s kubectl get pods -A -l %s=%s -o json", kubeconfig, core.LabelAppManagedBy, core.LabelManagedBy))
	if err != nil {
		return nil
	}
	var resp struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(out, &resp) != nil {
		return nil
	}

	var pods []K8sPod
	for _, item := range resp.Items {
		pods = append(pods, K8sPod{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			Status:    item.Status.Phase,
			Node:      item.Spec.NodeName,
		})
	}
	return pods
}

func fetchServices(ctx context.Context, ssh core.SSHClient, kubeconfig string) []K8sService {
	out, err := ssh.Run(ctx, fmt.Sprintf("%s kubectl get services -A -l %s=%s -o json", kubeconfig, core.LabelAppManagedBy, core.LabelManagedBy))
	if err != nil {
		return nil
	}
	var resp struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Type      string `json:"type"`
				ClusterIP string `json:"clusterIP"`
				Ports     []struct {
					Port     int    `json:"port"`
					Protocol string `json:"protocol"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	if json.Unmarshal(out, &resp) != nil {
		return nil
	}

	var services []K8sService
	for _, item := range resp.Items {
		var ports []string
		for _, p := range item.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}
		services = append(services, K8sService{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			Type:      item.Spec.Type,
			ClusterIP: item.Spec.ClusterIP,
			Ports:     strings.Join(ports, ","),
		})
	}
	return services
}
