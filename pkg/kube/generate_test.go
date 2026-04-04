package kube

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func mustNames(t *testing.T) *utils.Names {
	t.Helper()
	n, err := utils.NewNames("myapp", "production")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}
	return n
}

// splitDocs splits multi-doc YAML on "---" separators and returns non-empty documents.
func splitDocs(yaml string) []string {
	parts := strings.Split(yaml, "---")
	var docs []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			docs = append(docs, p)
		}
	}
	return docs
}

func TestBasicDeployment(t *testing.T) {
	names := mustNames(t)
	spec := ServiceSpec{
		Name:     "web",
		Image:    "nginx",
		Port:     80,
		Replicas: 2,
	}

	yamlStr, kind, err := GenerateYAML(spec, names, nil)
	if err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}
	if kind != "deployment" {
		t.Fatalf("expected workloadKind=deployment, got %q", kind)
	}

	docs := splitDocs(yamlStr)
	if len(docs) != 2 {
		t.Fatalf("expected 2 YAML docs, got %d", len(docs))
	}

	var dep appsv1.Deployment
	if err := sigsyaml.Unmarshal([]byte(docs[0]), &dep); err != nil {
		t.Fatalf("unmarshal Deployment: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 2 {
		t.Fatalf("expected replicas=2, got %v", dep.Spec.Replicas)
	}
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	if containers[0].Image != "nginx" {
		t.Errorf("expected image=nginx, got %q", containers[0].Image)
	}
	if len(containers[0].Ports) != 1 || containers[0].Ports[0].ContainerPort != 80 {
		t.Errorf("expected container port 80, got %v", containers[0].Ports)
	}

	var svc corev1.Service
	if err := sigsyaml.Unmarshal([]byte(docs[1]), &svc); err != nil {
		t.Fatalf("unmarshal Service: %v", err)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 80 {
		t.Errorf("expected service port 80, got %v", svc.Spec.Ports)
	}
}

func TestStatefulSet(t *testing.T) {
	names := mustNames(t)
	spec := ServiceSpec{
		Name:     "db",
		Image:    "postgres:17",
		Port:     5432,
		Replicas: 3, // should be forced to 1
		Managed:  true,
	}

	yamlStr, kind, err := GenerateYAML(spec, names, nil)
	if err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}
	if kind != "statefulset" {
		t.Fatalf("expected workloadKind=statefulset, got %q", kind)
	}

	docs := splitDocs(yamlStr)
	if len(docs) != 2 {
		t.Fatalf("expected 2 YAML docs, got %d", len(docs))
	}

	var ss appsv1.StatefulSet
	if err := sigsyaml.Unmarshal([]byte(docs[0]), &ss); err != nil {
		t.Fatalf("unmarshal StatefulSet: %v", err)
	}
	if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 1 {
		t.Fatalf("expected replicas forced to 1, got %v", ss.Spec.Replicas)
	}
}

func TestNoPort(t *testing.T) {
	names := mustNames(t)
	spec := ServiceSpec{
		Name:  "worker",
		Image: "myworker:latest",
		Port:  0,
	}

	yamlStr, _, err := GenerateYAML(spec, names, nil)
	if err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}

	docs := splitDocs(yamlStr)
	if len(docs) != 2 {
		t.Fatalf("expected 2 YAML docs, got %d", len(docs))
	}

	var svc corev1.Service
	if err := sigsyaml.Unmarshal([]byte(docs[1]), &svc); err != nil {
		t.Fatalf("unmarshal Service: %v", err)
	}
	if svc.Spec.ClusterIP != "None" {
		t.Errorf("expected ClusterIP=None, got %q", svc.Spec.ClusterIP)
	}
	if len(svc.Spec.Ports) != 0 {
		t.Errorf("expected no ports, got %v", svc.Spec.Ports)
	}
}

func TestCommandWrapping(t *testing.T) {
	names := mustNames(t)
	spec := ServiceSpec{
		Name:    "web",
		Image:   "rails:latest",
		Port:    3000,
		Command: "bundle exec rails s",
	}

	yamlStr, _, err := GenerateYAML(spec, names, nil)
	if err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}

	docs := splitDocs(yamlStr)
	var dep appsv1.Deployment
	if err := sigsyaml.Unmarshal([]byte(docs[0]), &dep); err != nil {
		t.Fatalf("unmarshal Deployment: %v", err)
	}

	c := dep.Spec.Template.Spec.Containers[0]
	if len(c.Command) != 2 || c.Command[0] != "/bin/sh" || c.Command[1] != "-c" {
		t.Errorf("expected Command=[\"/bin/sh\", \"-c\"], got %v", c.Command)
	}
	if len(c.Args) != 1 || c.Args[0] != "bundle exec rails s" {
		t.Errorf("expected Args=[\"bundle exec rails s\"], got %v", c.Args)
	}
}

func TestSecretReferences(t *testing.T) {
	names := mustNames(t)
	spec := ServiceSpec{
		Name:       "web",
		Image:      "myapp:latest",
		Port:       3000,
		Secrets:    []string{"DB_PASSWORD", "RAILS_KEY"},
		SecretName: "secrets",
	}

	yamlStr, _, err := GenerateYAML(spec, names, nil)
	if err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}

	docs := splitDocs(yamlStr)
	var dep appsv1.Deployment
	if err := sigsyaml.Unmarshal([]byte(docs[0]), &dep); err != nil {
		t.Fatalf("unmarshal Deployment: %v", err)
	}

	envVars := dep.Spec.Template.Spec.Containers[0].Env
	wantSecrets := map[string]string{
		"DB_PASSWORD": "secrets",
		"RAILS_KEY":   "secrets",
	}
	found := make(map[string]bool)
	for _, ev := range envVars {
		if ev.ValueFrom != nil && ev.ValueFrom.SecretKeyRef != nil {
			ref := ev.ValueFrom.SecretKeyRef
			expectedSecret, ok := wantSecrets[ev.Name]
			if !ok {
				t.Errorf("unexpected secret env var %q", ev.Name)
				continue
			}
			if ref.Name != expectedSecret {
				t.Errorf("env %q: expected secret name %q, got %q", ev.Name, expectedSecret, ref.Name)
			}
			if ref.Key != ev.Name {
				t.Errorf("env %q: expected key %q, got %q", ev.Name, ev.Name, ref.Key)
			}
			found[ev.Name] = true
		}
	}
	for key := range wantSecrets {
		if !found[key] {
			t.Errorf("missing secret env var %q", key)
		}
	}
}

func TestHealthCheck(t *testing.T) {
	names := mustNames(t)
	spec := ServiceSpec{
		Name:       "web",
		Image:      "myapp:latest",
		Port:       3000,
		HealthPath: "/up",
	}

	yamlStr, _, err := GenerateYAML(spec, names, nil)
	if err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}

	docs := splitDocs(yamlStr)
	var dep appsv1.Deployment
	if err := sigsyaml.Unmarshal([]byte(docs[0]), &dep); err != nil {
		t.Fatalf("unmarshal Deployment: %v", err)
	}

	c := dep.Spec.Template.Spec.Containers[0]
	if c.ReadinessProbe == nil {
		t.Fatal("expected readinessProbe, got nil")
	}
	probe := c.ReadinessProbe
	if probe.HTTPGet == nil {
		t.Fatal("expected HTTPGet probe, got nil")
	}
	if probe.HTTPGet.Path != "/up" {
		t.Errorf("expected probe path /up, got %q", probe.HTTPGet.Path)
	}
	if probe.HTTPGet.Port.IntValue() != 3000 {
		t.Errorf("expected probe port 3000, got %d", probe.HTTPGet.Port.IntValue())
	}
}

func TestNodeSelector(t *testing.T) {
	names := mustNames(t)
	spec := ServiceSpec{
		Name:   "db",
		Image:  "postgres:17",
		Port:   5432,
		Server: "master",
	}

	yamlStr, _, err := GenerateYAML(spec, names, nil)
	if err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}

	docs := splitDocs(yamlStr)
	var dep appsv1.Deployment
	if err := sigsyaml.Unmarshal([]byte(docs[0]), &dep); err != nil {
		t.Fatalf("unmarshal Deployment: %v", err)
	}

	nodeSelector := dep.Spec.Template.Spec.NodeSelector
	if nodeSelector == nil {
		t.Fatal("expected nodeSelector, got nil")
	}
	if nodeSelector[utils.LabelNvoiRole] != "master" {
		t.Errorf("expected nodeSelector[%q]=master, got %q", utils.LabelNvoiRole, nodeSelector[utils.LabelNvoiRole])
	}
}

func TestNamedVolumes(t *testing.T) {
	names := mustNames(t)
	spec := ServiceSpec{
		Name:    "db",
		Image:   "postgres:17",
		Port:    5432,
		Volumes: []string{"pgdata:/var/lib/postgresql/data"},
	}
	managedVolPaths := map[string]string{
		"pgdata": "/mnt/data/nvoi-myapp-production-pgdata",
	}

	yamlStr, _, err := GenerateYAML(spec, names, managedVolPaths)
	if err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}

	docs := splitDocs(yamlStr)
	var dep appsv1.Deployment
	if err := sigsyaml.Unmarshal([]byte(docs[0]), &dep); err != nil {
		t.Fatalf("unmarshal Deployment: %v", err)
	}

	podSpec := dep.Spec.Template.Spec

	// Check volume
	if len(podSpec.Volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(podSpec.Volumes))
	}
	vol := podSpec.Volumes[0]
	if vol.HostPath == nil {
		t.Fatal("expected hostPath volume, got nil")
	}
	if vol.HostPath.Path != "/mnt/data/nvoi-myapp-production-pgdata" {
		t.Errorf("expected hostPath=/mnt/data/nvoi-myapp-production-pgdata, got %q", vol.HostPath.Path)
	}

	// Check volumeMount
	c := podSpec.Containers[0]
	if len(c.VolumeMounts) != 1 {
		t.Fatalf("expected 1 volumeMount, got %d", len(c.VolumeMounts))
	}
	mount := c.VolumeMounts[0]
	if mount.MountPath != "/var/lib/postgresql/data" {
		t.Errorf("expected mountPath=/var/lib/postgresql/data, got %q", mount.MountPath)
	}
	if mount.Name != vol.Name {
		t.Errorf("expected volumeMount name %q to match volume name %q", mount.Name, vol.Name)
	}
}
