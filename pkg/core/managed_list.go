package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/managed"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ManagedService represents a managed service discovered from cluster labels.
type ManagedService struct {
	Name        string   `json:"name"`
	ManagedKind string   `json:"managed_kind"`
	Category    string   `json:"category"`
	Image       string   `json:"image"`
	Ready       string   `json:"ready"`
	Children    []string `json:"children,omitempty"`
}

type ManagedListRequest struct {
	Cluster
	Kind string // filter by managed kind ("postgres", "claude", or "" for all)
}

// ManagedList discovers managed services in the cluster by the nvoi/managed-kind label.
func ManagedList(ctx context.Context, req ManagedListRequest) ([]ManagedService, error) {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	selector := utils.LabelNvoiManagedKind
	if req.Kind != "" {
		selector = utils.LabelNvoiManagedKind + "=" + req.Kind
	}

	// Query deployments with the managed-kind label.
	cmd := "get deployments -l " + selector + " -o json"
	out, err := ssh.Run(ctx, kubectlCmd(ns, cmd))
	if err != nil {
		return nil, err
	}

	var result kube.WorkloadList
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, err
	}

	// Also check statefulsets.
	cmd = "get statefulsets -l " + selector + " -o json"
	out2, err := ssh.Run(ctx, kubectlCmd(ns, cmd))
	if err == nil {
		var ssResult kube.WorkloadList
		if json.Unmarshal(out2, &ssResult) == nil {
			result.Items = append(result.Items, ssResult.Items...)
		}
	}

	var services []ManagedService
	for _, item := range result.Items {
		image := ""
		if len(item.Spec.Template.Spec.Containers) > 0 {
			image = item.Spec.Template.Spec.Containers[0].Image
		}
		kind := item.Metadata.Labels[utils.LabelNvoiManagedKind]
		svc := ManagedService{
			Name:        item.Metadata.Name,
			ManagedKind: kind,
			Image:       image,
			Ready:       readyString(item.Status.ReadyReplicas, item.Spec.Replicas),
		}
		// Enrich with category and children from managed definitions.
		if def, ok := managed.Get(kind); ok {
			svc.Category = def.Category()
			shape := def.Shape(item.Metadata.Name)
			svc.Children = shape.OwnedChildren
		}
		services = append(services, svc)
	}
	return services, nil
}

func kubectlCmd(ns, command string) string {
	return "KUBECONFIG=/home/" + utils.DefaultUser + "/.kube/config kubectl -n " + ns + " " + command
}

func readyString(ready, total int) string {
	return fmt.Sprintf("%d/%d", ready, total)
}
