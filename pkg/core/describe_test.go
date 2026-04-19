package core

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// newKC builds a *kube.Client backed by the client-go fake typed clientset,
// pre-populated with objs.
func newKC(objs ...runtime.Object) *kube.Client {
	cs := k8sfake.NewSimpleClientset(objs...)
	return kube.NewForTest(cs)
}

// managedLabels annotates an object as nvoi-managed so the NvoiSelector label
// filter matches.
func managedLabels(extra ...string) map[string]string {
	labels := map[string]string{utils.LabelAppManagedBy: utils.LabelManagedBy}
	for i := 0; i+1 < len(extra); i += 2 {
		labels[extra[i]] = extra[i+1]
	}
	return labels
}

func TestDescribeNodes(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "nvoi-myapp-prod-master",
			Labels: map[string]string{utils.LabelNvoiRole: "master"},
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.1.1"},
				{Type: corev1.NodeExternalIP, Address: "1.2.3.4"},
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
	kc := newKC(node)

	nodes := describeNodes(context.Background(), kc)
	if len(nodes) != 1 {
		t.Fatalf("len = %d, want 1", len(nodes))
	}
	n := nodes[0]
	if n.Name != "nvoi-myapp-prod-master" {
		t.Errorf("Name = %q", n.Name)
	}
	if n.Status != "Ready" {
		t.Errorf("Status = %q", n.Status)
	}
	if n.IP != "10.0.1.1" {
		t.Errorf("IP = %q", n.IP)
	}
	if n.Role != "master" {
		t.Errorf("Role = %q", n.Role)
	}
}

func TestDescribeWorkloads(t *testing.T) {
	two := int32(2)
	creation := metav1.NewTime(time.Now().Add(-time.Hour))
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "ns",
			CreationTimestamp: creation,
			Labels:            managedLabels(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &two,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Image: "nginx:latest"}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 2},
	}
	kc := newKC(dep)

	workloads := describeWorkloads(context.Background(), kc, "ns")
	if len(workloads) != 1 {
		t.Fatalf("len = %d, want 1", len(workloads))
	}
	w := workloads[0]
	if w.Name != "web" {
		t.Errorf("Name = %q", w.Name)
	}
	if w.Kind != "deployment" {
		t.Errorf("Kind = %q", w.Kind)
	}
	if w.Ready != "2/2" {
		t.Errorf("Ready = %q", w.Ready)
	}
	if w.Image != "nginx:latest" {
		t.Errorf("Image = %q", w.Image)
	}
}

func TestDescribePods(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc123", Namespace: "ns",
			Labels: managedLabels(),
		},
		Spec: corev1.PodSpec{NodeName: "nvoi-myapp-prod-master"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true, RestartCount: 0},
			},
		},
	}
	kc := newKC(pod)

	pods := describePods(context.Background(), kc, "ns")
	if len(pods) != 1 {
		t.Fatalf("len = %d, want 1", len(pods))
	}
	p := pods[0]
	if p.Name != "web-abc123" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.Status != "Running" {
		t.Errorf("Status = %q", p.Status)
	}
	if p.Node != "nvoi-myapp-prod-master" {
		t.Errorf("Node = %q", p.Node)
	}
	if p.Restarts != 0 {
		t.Errorf("Restarts = %d", p.Restarts)
	}
}

func TestDescribeServices(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "ns",
			Labels: managedLabels(),
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.43.0.100",
			Ports:     []corev1.ServicePort{{Port: 3000, Protocol: corev1.ProtocolTCP}},
		},
	}
	kc := newKC(svc)

	services := describeServices(context.Background(), kc, "ns")
	if len(services) != 1 {
		t.Fatalf("len = %d, want 1", len(services))
	}
	s := services[0]
	if s.Name != "web" {
		t.Errorf("Name = %q", s.Name)
	}
	if s.Type != "ClusterIP" {
		t.Errorf("Type = %q", s.Type)
	}
	if s.ClusterIP != "10.43.0.100" {
		t.Errorf("ClusterIP = %q", s.ClusterIP)
	}
	if s.Ports != "3000/TCP" {
		t.Errorf("Ports = %q", s.Ports)
	}
}

func TestDescribeCrons(t *testing.T) {
	cron := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "backup", Namespace: "ns",
			Labels: managedLabels(),
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 1 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Image: "busybox:latest"}},
						},
					},
				},
			},
		},
		Status: batchv1.CronJobStatus{
			Active: []corev1.ObjectReference{{}},
		},
	}
	kc := newKC(cron)

	crons := describeCrons(context.Background(), kc, "ns")
	if len(crons) != 1 {
		t.Fatalf("len = %d, want 1", len(crons))
	}
	c := crons[0]
	if c.Name != "backup" {
		t.Errorf("Name = %q", c.Name)
	}
	if c.Schedule != "0 1 * * *" {
		t.Errorf("Schedule = %q", c.Schedule)
	}
	if c.Image != "busybox:latest" {
		t.Errorf("Image = %q", c.Image)
	}
	if c.Status != "active" {
		t.Errorf("Status = %q", c.Status)
	}
}

func TestDescribeCrons_Idle(t *testing.T) {
	cron := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "backup", Namespace: "ns",
			Labels: managedLabels(),
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 1 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Image: "busybox"}},
						},
					},
				},
			},
		},
	}
	kc := newKC(cron)

	crons := describeCrons(context.Background(), kc, "ns")
	if len(crons) != 1 {
		t.Fatalf("len = %d", len(crons))
	}
	if crons[0].Status != "idle" {
		t.Errorf("Status = %q, want idle", crons[0].Status)
	}
}

func TestDescribeTunnelAgents(t *testing.T) {
	ns := "nvoi-myapp-prod"
	creation := metav1.NewTime(time.Now().Add(-time.Hour))
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "cloudflared-abc",
			Namespace:         ns,
			Labels:            map[string]string{"app.kubernetes.io/name": "cloudflared"},
			CreationTimestamp: creation,
		},
		Spec: corev1.PodSpec{NodeName: "nvoi-myapp-prod-master"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 2},
			},
		},
	}
	kc := newKC(pod)

	agents, err := describeTunnelAgents(context.Background(), kc, ns)
	if err != nil {
		t.Fatalf("describeTunnelAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len = %d, want 1", len(agents))
	}
	a := agents[0]
	if a.Name != "cloudflared-abc" {
		t.Errorf("Name = %q", a.Name)
	}
	if a.Status != "Running" {
		t.Errorf("Status = %q", a.Status)
	}
	if a.Restarts != 2 {
		t.Errorf("Restarts = %d", a.Restarts)
	}
	if a.Node != "nvoi-myapp-prod-master" {
		t.Errorf("Node = %q", a.Node)
	}
}

func TestDescribeTunnelAgents_Empty(t *testing.T) {
	kc := newKC()
	agents, err := describeTunnelAgents(context.Background(), kc, "nvoi-myapp-prod")
	if err != nil {
		t.Fatalf("describeTunnelAgents empty: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestDescribeTunnelAgents_WaitingPodStatus(t *testing.T) {
	ns := "nvoi-myapp-prod"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloudflared-pending",
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/name": "cloudflared"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
				}},
			},
		},
	}
	kc := newKC(pod)

	agents, err := describeTunnelAgents(context.Background(), kc, ns)
	if err != nil {
		t.Fatalf("describeTunnelAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len = %d, want 1", len(agents))
	}
	// Waiting reason takes priority over phase.
	if agents[0].Status != "ImagePullBackOff" {
		t.Errorf("Status = %q, want ImagePullBackOff", agents[0].Status)
	}
}

func TestDescribeWorkloads_FiltersUnmanaged(t *testing.T) {
	// An unlabeled deployment must not show up — only nvoi-managed workloads.
	unlabeled := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Image: "x"}}},
			},
		},
	}
	managed := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns", Labels: managedLabels()},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Image: "nginx"}}},
			},
		},
	}
	kc := newKC(unlabeled, managed)

	workloads := describeWorkloads(context.Background(), kc, "ns")
	if len(workloads) != 1 || workloads[0].Name != "web" {
		t.Errorf("unmanaged not filtered: %+v", workloads)
	}
}
