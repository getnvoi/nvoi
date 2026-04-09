package kube

// WorkloadItem is the JSON shape for a Deployment or StatefulSet from kubectl get -o json.
// Used by any code that queries workloads (describe, managed list, etc.).
type WorkloadItem struct {
	Metadata struct {
		Name              string            `json:"name"`
		Labels            map[string]string `json:"labels"`
		CreationTimestamp string            `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Replicas int `json:"replicas"`
		Template struct {
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas int `json:"readyReplicas"`
	} `json:"status"`
}

// WorkloadList is the JSON shape for kubectl get deployments/statefulsets -o json.
type WorkloadList struct {
	Items []WorkloadItem `json:"items"`
}
