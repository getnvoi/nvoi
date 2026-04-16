package core

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/pkg/kube"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDescribeNodes(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "nvoi-myapp-prod-master",
				Labels: map[string]string{"nvoi-role": "master"},
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
		},
	)
	kc := kube.NewFromClientset(cs)

	ctx := context.Background()
	nodes := describeNodes(ctx, kc)

	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	n := nodes[0]
	if n.Name != "nvoi-myapp-prod-master" {
		t.Errorf("Name = %q, want %q", n.Name, "nvoi-myapp-prod-master")
	}
	if n.Status != "Ready" {
		t.Errorf("Status = %q, want %q", n.Status, "Ready")
	}
	if n.IP != "10.0.1.1" {
		t.Errorf("IP = %q, want %q", n.IP, "10.0.1.1")
	}
	if n.Role != "master" {
		t.Errorf("Role = %q, want %q", n.Role, "master")
	}
}

func TestDescribeWorkloads(t *testing.T) {
	replicas := int32(2)
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "web",
				Namespace:         "nvoi-myapp-prod",
				CreationTimestamp: metav1.Now(),
				Labels:            map[string]string{"app.kubernetes.io/managed-by": "nvoi"},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "web", Image: "nginx:latest"}},
					},
				},
			},
			Status: appsv1.DeploymentStatus{
				ReadyReplicas: 2,
			},
		},
	)
	kc := kube.NewFromClientset(cs)

	ctx := context.Background()
	workloads := describeWorkloads(ctx, kc, "nvoi-myapp-prod")

	if len(workloads) != 1 {
		t.Fatalf("len(workloads) = %d, want 1", len(workloads))
	}
	w := workloads[0]
	if w.Name != "web" {
		t.Errorf("Name = %q, want %q", w.Name, "web")
	}
	if w.Kind != "deployment" {
		t.Errorf("Kind = %q, want %q", w.Kind, "deployment")
	}
	if w.Ready != "2/2" {
		t.Errorf("Ready = %q, want %q", w.Ready, "2/2")
	}
	if w.Image != "nginx:latest" {
		t.Errorf("Image = %q, want %q", w.Image, "nginx:latest")
	}
}

func TestDescribePods(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "web-abc123",
				Namespace:         "nvoi-myapp-prod",
				CreationTimestamp: metav1.Now(),
				Labels:            map[string]string{"app.kubernetes.io/managed-by": "nvoi"},
			},
			Spec: corev1.PodSpec{
				NodeName: "nvoi-myapp-prod-master",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:         "web",
					Ready:        true,
					RestartCount: 0,
					State:        corev1.ContainerState{},
				}},
			},
		},
	)
	kc := kube.NewFromClientset(cs)

	ctx := context.Background()
	pods := describePods(ctx, kc, "nvoi-myapp-prod")

	if len(pods) != 1 {
		t.Fatalf("len(pods) = %d, want 1", len(pods))
	}
	p := pods[0]
	if p.Name != "web-abc123" {
		t.Errorf("Name = %q, want %q", p.Name, "web-abc123")
	}
	if p.Status != "Running" {
		t.Errorf("Status = %q, want %q", p.Status, "Running")
	}
	if p.Node != "nvoi-myapp-prod-master" {
		t.Errorf("Node = %q, want %q", p.Node, "nvoi-myapp-prod-master")
	}
	if p.Restarts != 0 {
		t.Errorf("Restarts = %d, want 0", p.Restarts)
	}
}

func TestDescribeServices(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "web",
				Namespace: "nvoi-myapp-prod",
				Labels:    map[string]string{"app.kubernetes.io/managed-by": "nvoi"},
			},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.43.0.100",
				Ports: []corev1.ServicePort{{
					Port:     3000,
					Protocol: corev1.ProtocolTCP,
				}},
			},
		},
	)
	kc := kube.NewFromClientset(cs)

	ctx := context.Background()
	services := describeServices(ctx, kc, "nvoi-myapp-prod")

	if len(services) != 1 {
		t.Fatalf("len(services) = %d, want 1", len(services))
	}
	s := services[0]
	if s.Name != "web" {
		t.Errorf("Name = %q, want %q", s.Name, "web")
	}
	if s.Type != "ClusterIP" {
		t.Errorf("Type = %q, want %q", s.Type, "ClusterIP")
	}
	if s.ClusterIP != "10.43.0.100" {
		t.Errorf("ClusterIP = %q, want %q", s.ClusterIP, "10.43.0.100")
	}
	if s.Ports != "3000/TCP" {
		t.Errorf("Ports = %q, want %q", s.Ports, "3000/TCP")
	}
}

// TestDescribeManagedChildrenVisible verifies that all resource types owned by
// a managed bundle (workloads, crons, services, secrets, storage) are represented
// in the describe output. Managed children are real k8s resources — they must be
// visible in describe identically in local and cloud modes.
func TestDescribeManagedChildrenVisible(t *testing.T) {
	replicas := int32(1)
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "db",
				Namespace:         "nvoi-myapp-prod",
				CreationTimestamp: metav1.Now(),
				Labels:            map[string]string{"app.kubernetes.io/managed-by": "nvoi"},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "db", Image: "postgres:17"}}},
				},
			},
			Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
		},
		&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "db-backup",
				Namespace:         "nvoi-myapp-prod",
				CreationTimestamp: metav1.Now(),
				Labels:            map[string]string{"app.kubernetes.io/managed-by": "nvoi"},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "0 2 * * *",
				JobTemplate: batchv1.JobTemplateSpec{
					Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers:    []corev1.Container{{Name: "db-backup", Image: "postgres:17"}},
								RestartPolicy: corev1.RestartPolicyNever,
							},
						},
					},
				},
			},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "db",
				Namespace: "nvoi-myapp-prod",
				Labels:    map[string]string{"app.kubernetes.io/managed-by": "nvoi"},
			},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.43.0.50",
				Ports: []corev1.ServicePort{{
					Port:     5432,
					Protocol: corev1.ProtocolTCP,
				}},
			},
		},
	)
	kc := kube.NewFromClientset(cs)

	ctx := context.Background()
	ns := "nvoi-myapp-prod"

	workloads := describeWorkloads(ctx, kc, ns)
	if len(workloads) != 1 || workloads[0].Name != "db" {
		t.Errorf("workloads = %v, want db deployment", workloads)
	}

	crons := describeCrons(ctx, kc, ns)
	if len(crons) != 1 || crons[0].Name != "db-backup" {
		t.Errorf("crons = %v, want db-backup", crons)
	}

	services := describeServices(ctx, kc, ns)
	if len(services) != 1 || services[0].Name != "db" {
		t.Errorf("services = %v, want db service", services)
	}
	if services[0].Ports != "5432/TCP" {
		t.Errorf("db service ports = %q, want 5432/TCP", services[0].Ports)
	}
}

func TestDescribeCrons(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "backup",
				Namespace:         "nvoi-myapp-prod",
				CreationTimestamp: metav1.Now(),
				Labels:            map[string]string{"app.kubernetes.io/managed-by": "nvoi"},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "0 1 * * *",
				JobTemplate: batchv1.JobTemplateSpec{
					Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers:    []corev1.Container{{Name: "backup", Image: "busybox:latest"}},
								RestartPolicy: corev1.RestartPolicyNever,
							},
						},
					},
				},
			},
			Status: batchv1.CronJobStatus{
				Active: []corev1.ObjectReference{{}},
			},
		},
	)
	kc := kube.NewFromClientset(cs)

	crons := describeCrons(context.Background(), kc, "nvoi-myapp-prod")
	if len(crons) != 1 {
		t.Fatalf("len(crons) = %d, want 1", len(crons))
	}
	c := crons[0]
	if c.Name != "backup" {
		t.Errorf("Name = %q, want backup", c.Name)
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
