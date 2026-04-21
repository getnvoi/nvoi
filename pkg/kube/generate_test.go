package kube

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// asDeployment unwraps the typed workload returned by BuildService.
func asDeployment(t *testing.T, obj runtime.Object) *appsv1.Deployment {
	t.Helper()
	dep, ok := obj.(*appsv1.Deployment)
	if !ok {
		t.Fatalf("expected *Deployment, got %T", obj)
	}
	return dep
}

func asStatefulSet(t *testing.T, obj runtime.Object) *appsv1.StatefulSet {
	t.Helper()
	ss, ok := obj.(*appsv1.StatefulSet)
	if !ok {
		t.Fatalf("expected *StatefulSet, got %T", obj)
	}
	return ss
}

func TestBuildService_BasicDeployment(t *testing.T) {
	workload, svc, kind, err := BuildService(ServiceSpec{
		Name:     "web",
		Image:    "nginx:latest",
		Port:     80,
		Replicas: 2,
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if kind != "deployment" {
		t.Errorf("kind = %q", kind)
	}
	dep := asDeployment(t, workload)
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 2 {
		t.Errorf("replicas = %v", dep.Spec.Replicas)
	}
	if svc.Spec.Ports[0].Port != 80 {
		t.Errorf("svc port = %d", svc.Spec.Ports[0].Port)
	}
	ct := dep.Spec.Template.Spec.Containers[0]
	if ct.Image != "nginx:latest" {
		t.Errorf("image = %q", ct.Image)
	}
	if ct.Ports[0].ContainerPort != 80 {
		t.Errorf("containerPort = %d", ct.Ports[0].ContainerPort)
	}
}

func TestBuildService_StatefulSetForManagedVolume(t *testing.T) {
	workload, svc, kind, err := BuildService(ServiceSpec{
		Name:    "db",
		Image:   "postgres:17",
		Port:    5432,
		Volumes: []string{"pgdata:/var/lib/postgresql/data"},
		Managed: true,
	}, mustNames(t), map[string]string{"pgdata": "/mnt/data/pgdata"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if kind != "statefulset" {
		t.Errorf("kind = %q", kind)
	}
	ss := asStatefulSet(t, workload)
	if ss.Spec.ServiceName != "db" {
		t.Errorf("serviceName = %q", ss.Spec.ServiceName)
	}
	if len(ss.Spec.Template.Spec.Volumes) != 1 {
		t.Errorf("volumes = %v", ss.Spec.Template.Spec.Volumes)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 5432 {
		t.Errorf("svc ports = %+v", svc.Spec.Ports)
	}
}

func TestBuildService_NoPortHeadlessService(t *testing.T) {
	_, svc, _, err := BuildService(ServiceSpec{
		Name:  "worker",
		Image: "worker:v1",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if svc.Spec.ClusterIP != "None" {
		t.Errorf("expected headless service when no port, got %q", svc.Spec.ClusterIP)
	}
	if len(svc.Spec.Ports) != 0 {
		t.Errorf("expected no ports, got %v", svc.Spec.Ports)
	}
}

func TestBuildService_CommandWrapping(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:    "worker",
		Image:   "busybox",
		Command: "echo hi",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	ct := dep.Spec.Template.Spec.Containers[0]
	if len(ct.Command) != 2 || ct.Command[0] != "/bin/sh" || ct.Command[1] != "-c" {
		t.Errorf("command = %v", ct.Command)
	}
	if len(ct.Args) != 1 || ct.Args[0] != "echo hi" {
		t.Errorf("args = %v", ct.Args)
	}
}

func TestBuildService_TCPProbeByDefault(t *testing.T) {
	// Port set, no HealthPath → readiness probe is TCP connect on the port.
	// This makes Ready mean "accepting connections", not just "container
	// Running" — critical for depends_on ordering.
	workload, _, _, err := BuildService(ServiceSpec{
		Name:  "db",
		Image: "postgres:17",
		Port:  5432,
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	probe := asDeployment(t, workload).Spec.Template.Spec.Containers[0].ReadinessProbe
	if probe == nil {
		t.Fatal("expected default TCP probe when port is set")
	}
	if probe.TCPSocket == nil {
		t.Errorf("expected TCPSocket probe, got %+v", probe)
	}
	if probe.TCPSocket.Port.IntValue() != 5432 {
		t.Errorf("probe port = %v, want 5432", probe.TCPSocket.Port)
	}
	if probe.HTTPGet != nil {
		t.Error("HTTPGet should be nil when no HealthPath")
	}
}

func TestBuildService_HTTPProbeOverridesTCP(t *testing.T) {
	// Explicit HealthPath → HTTP GET probe, not TCP.
	workload, _, _, err := BuildService(ServiceSpec{
		Name: "web", Image: "nginx", Port: 80, HealthPath: "/healthz",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	probe := asDeployment(t, workload).Spec.Template.Spec.Containers[0].ReadinessProbe
	if probe.HTTPGet == nil {
		t.Fatal("HealthPath should yield HTTP probe")
	}
	if probe.TCPSocket != nil {
		t.Error("TCP probe must not coexist with HTTP probe")
	}
}

func TestBuildService_NoProbeWithoutPort(t *testing.T) {
	// No port → no probe at all (headless service, nothing to check).
	workload, _, _, err := BuildService(ServiceSpec{
		Name: "worker", Image: "worker:v1",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	probe := asDeployment(t, workload).Spec.Template.Spec.Containers[0].ReadinessProbe
	if probe != nil {
		t.Errorf("no port, expected no probe, got %+v", probe)
	}
}

func TestBuildService_HealthCheckProbe(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:       "web",
		Image:      "nginx",
		Port:       80,
		HealthPath: "/healthz",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	probe := dep.Spec.Template.Spec.Containers[0].ReadinessProbe
	if probe == nil {
		t.Fatal("readiness probe missing")
	}
	if probe.HTTPGet.Path != "/healthz" {
		t.Errorf("probe path = %q", probe.HTTPGet.Path)
	}
	if probe.HTTPGet.Port.IntValue() != 80 {
		t.Errorf("probe port = %v", probe.HTTPGet.Port)
	}
}

func TestBuildService_SecretRef(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:          "web",
		Image:         "nginx",
		SvcSecrets:    []string{"API_KEY"},
		SvcSecretName: "web-secrets",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	env := dep.Spec.Template.Spec.Containers[0].Env
	if len(env) != 1 || env[0].Name != "API_KEY" {
		t.Fatalf("env = %+v", env)
	}
	ref := env[0].ValueFrom.SecretKeyRef
	if ref.Name != "web-secrets" || ref.Key != "API_KEY" {
		t.Errorf("secretKeyRef = %+v", ref)
	}
}

func TestBuildService_SecretRefAliased(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:          "web",
		Image:         "nginx",
		SvcSecrets:    []string{"DATABASE_URL=MAIN_DATABASE_URL"},
		SvcSecretName: "web-secrets",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	env := asDeployment(t, workload).Spec.Template.Spec.Containers[0].Env
	if env[0].Name != "DATABASE_URL" || env[0].ValueFrom.SecretKeyRef.Key != "MAIN_DATABASE_URL" {
		t.Errorf("aliased ref wired wrong: %+v", env[0])
	}
}

func TestBuildService_NodeSelector_SingleServer(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:    "web",
		Image:   "nginx",
		Servers: []string{"worker-1"},
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	if dep.Spec.Template.Spec.NodeSelector[utils.LabelNvoiRole] != "worker-1" {
		t.Errorf("nodeSelector = %v", dep.Spec.Template.Spec.NodeSelector)
	}
}

func TestBuildService_MultiServerAffinity(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:    "web",
		Image:   "nginx",
		Servers: []string{"worker-1", "worker-2"},
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	aff := dep.Spec.Template.Spec.Affinity
	if aff == nil || aff.NodeAffinity == nil {
		t.Fatal("nodeAffinity missing for multi-server")
	}
	terms := aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 {
		t.Fatalf("match expressions = %+v", terms)
	}
	if len(terms[0].MatchExpressions[0].Values) != 2 {
		t.Errorf("values = %v", terms[0].MatchExpressions[0].Values)
	}
	if len(dep.Spec.Template.Spec.TopologySpreadConstraints) != 1 {
		t.Error("topologySpreadConstraints required for multi-server even distribution")
	}
}

func TestBuildService_DefaultServerIsMaster(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:  "web",
		Image: "nginx",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	if dep.Spec.Template.Spec.NodeSelector[utils.LabelNvoiRole] != "master" {
		t.Errorf("default server should pin to master, got %v", dep.Spec.Template.Spec.NodeSelector)
	}
}

func TestBuildService_NamedVolumes(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:    "web",
		Image:   "nginx",
		Volumes: []string{"cache:/cache"},
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	vols := dep.Spec.Template.Spec.Volumes
	if len(vols) != 1 {
		t.Fatalf("volumes = %v", vols)
	}
	if vols[0].HostPath == nil {
		t.Errorf("named volume must use HostPath: %+v", vols[0])
	}
	mounts := dep.Spec.Template.Spec.Containers[0].VolumeMounts
	if len(mounts) != 1 || mounts[0].MountPath != "/cache" {
		t.Errorf("mounts = %+v", mounts)
	}
}

func TestParseSecretRef_Plain(t *testing.T) {
	env, key := ParseSecretRef("API_KEY")
	if env != "API_KEY" || key != "API_KEY" {
		t.Errorf("plain: env=%q key=%q", env, key)
	}
}

func TestParseSecretRef_Aliased(t *testing.T) {
	env, key := ParseSecretRef("DB_URL=MAIN_DATABASE_URL")
	if env != "DB_URL" || key != "MAIN_DATABASE_URL" {
		t.Errorf("aliased: env=%q key=%q", env, key)
	}
}

// Sanity: the typed objects we build carry the managed-by label so orphan
// detection can find them via NvoiSelector.
func TestBuildService_LabelsIncludeManagedBy(t *testing.T) {
	workload, svc, _, err := BuildService(ServiceSpec{Name: "web", Image: "nginx"}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	if dep.Labels[utils.LabelAppManagedBy] != utils.LabelManagedBy {
		t.Errorf("deployment missing managed-by: %v", dep.Labels)
	}
	if svc.Labels[utils.LabelAppManagedBy] != utils.LabelManagedBy {
		t.Errorf("service missing managed-by: %v", svc.Labels)
	}
	if dep.Spec.Template.Labels[utils.LabelAppName] == "" {
		t.Error("pod template missing app.kubernetes.io/name")
	}
}

// INVARIANT: imagePullSecrets is injected on the PodSpec iff
// PullSecretName is non-empty. The Secret it references is expected to
// live in the same namespace (the app ns) as the workload.
func TestBuildService_ImagePullSecrets(t *testing.T) {
	t.Run("injected when name set", func(t *testing.T) {
		workload, _, _, err := BuildService(ServiceSpec{
			Name:           "web",
			Image:          "ghcr.io/org/web:v1",
			Port:           80,
			Replicas:       2,
			PullSecretName: PullSecretName,
		}, mustNames(t), nil)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		dep := asDeployment(t, workload)
		ips := dep.Spec.Template.Spec.ImagePullSecrets
		if len(ips) != 1 || ips[0].Name != PullSecretName {
			t.Errorf("imagePullSecrets = %+v, want one entry named %q", ips, PullSecretName)
		}
	})
	t.Run("absent when name empty", func(t *testing.T) {
		workload, _, _, err := BuildService(ServiceSpec{
			Name:  "web",
			Image: "docker.io/library/nginx",
			Port:  80,
		}, mustNames(t), nil)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		dep := asDeployment(t, workload)
		if len(dep.Spec.Template.Spec.ImagePullSecrets) != 0 {
			t.Errorf("imagePullSecrets must be empty for public-registry pulls, got %+v",
				dep.Spec.Template.Spec.ImagePullSecrets)
		}
	})
}

// INVARIANT: nvoi/deploy-hash label lands on the Deployment/StatefulSet
// ObjectMeta — `kubectl get deploy -L nvoi/deploy-hash` surfaces which
// deploy last converged the workload. It must NOT appear on the pod-template
// labels: changing a pod-template label triggers a rolling restart, and for
// pull-only services (postgres:17, redis:7, etc.) whose image is static, that
// would restart a live database on every deploy, dropping DB connections for
// every dependent service. Built services restart naturally because their
// image tag changes every deploy (hash suffix). The label must NOT appear in
// the selector (matchLabels) — that would orphan old pods on every deploy.
func TestBuildService_DeployHashLabel(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:       "web",
		Image:      "nginx:1.27",
		Port:       80,
		Replicas:   2,
		DeployHash: "20260417-143022",
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	// Hash IS on workload metadata (observability).
	if got := dep.ObjectMeta.Labels[utils.LabelNvoiDeployHash]; got != "20260417-143022" {
		t.Errorf("workload label = %q, want 20260417-143022", got)
	}
	// Hash is NOT on pod-template — that would restart pull-only services every deploy.
	if got, ok := dep.Spec.Template.ObjectMeta.Labels[utils.LabelNvoiDeployHash]; ok {
		t.Errorf("pod template must NOT carry deploy-hash (causes unnecessary restarts for pull-only services); got %q", got)
	}
	// REGRESSION GUARD: the selector must NOT carry the hash. Including
	// it would mean every new deploy (new hash) selects a fresh empty
	// ReplicaSet and orphans the running pods.
	if _, ok := dep.Spec.Selector.MatchLabels[utils.LabelNvoiDeployHash]; ok {
		t.Errorf("selector must NOT include deploy-hash — would orphan pods every deploy: %v", dep.Spec.Selector.MatchLabels)
	}
}

func TestBuildService_DeployHashAbsent_NoLabel(t *testing.T) {
	workload, _, _, err := BuildService(ServiceSpec{
		Name:     "web",
		Image:    "nginx:1.27",
		Port:     80,
		Replicas: 2,
	}, mustNames(t), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dep := asDeployment(t, workload)
	if _, ok := dep.ObjectMeta.Labels[utils.LabelNvoiDeployHash]; ok {
		t.Error("empty DeployHash must not write the label key")
	}
}
