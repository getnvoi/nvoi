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

// PodItem is the JSON shape for a Pod from kubectl get pods -o json.
// Shared by rollout monitoring, describe, and any pod status parser.
type PodItem struct {
	Metadata struct {
		Name              string `json:"name"`
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status PodStatus `json:"status"`
}

// PodStatus is the status block of a k8s Pod.
type PodStatus struct {
	Phase             string            `json:"phase"`
	Conditions        []PodCondition    `json:"conditions"`
	ContainerStatuses []ContainerStatus `json:"containerStatuses"`
}

// PodCondition is a single condition on a Pod (e.g. PodScheduled).
type PodCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// ContainerStatus is the status of a single container in a Pod.
type ContainerStatus struct {
	Ready        bool           `json:"ready"`
	RestartCount int            `json:"restartCount"`
	State        ContainerState `json:"state"`
}

// ContainerState represents the current state of a container.
type ContainerState struct {
	Waiting    *ContainerStateWaiting    `json:"waiting"`
	Running    *struct{}                 `json:"running"`
	Terminated *ContainerStateTerminated `json:"terminated"`
}

// ContainerStateWaiting is the waiting state detail.
type ContainerStateWaiting struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// ContainerStateTerminated is the terminated state detail.
type ContainerStateTerminated struct {
	ExitCode int    `json:"exitCode"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
}

// PodList is the JSON shape for kubectl get pods -o json.
type PodList struct {
	Items []PodItem `json:"items"`
}

// EventItem is the JSON shape for a k8s Event from kubectl get events -o json.
type EventItem struct {
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// EventList is the JSON shape for kubectl get events -o json.
type EventList struct {
	Items []EventItem `json:"items"`
}
