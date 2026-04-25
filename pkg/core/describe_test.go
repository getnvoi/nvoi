package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
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

// ── DATABASES section (probe-driven, parallel) ──────────────────────────────

// fakeDBProvider is a minimal DatabaseProvider for the describe tests.
// It satisfies the interface only to the extent needed: ExecSQL is the
// one method describeDatabases calls. Everything else returns zero values.
type fakeDBProvider struct {
	execErr error
	execCnt int
}

func (f *fakeDBProvider) ValidateCredentials(context.Context) error { return nil }
func (f *fakeDBProvider) EnsureCredentials(context.Context, *kube.Client, provider.DatabaseRequest) (provider.DatabaseCredentials, error) {
	return provider.DatabaseCredentials{}, nil
}
func (f *fakeDBProvider) Reconcile(context.Context, provider.DatabaseRequest) (*provider.DatabasePlan, error) {
	return nil, nil
}
func (f *fakeDBProvider) Delete(context.Context, provider.DatabaseRequest) error { return nil }
func (f *fakeDBProvider) ExecSQL(_ context.Context, _ provider.DatabaseRequest, _ string) (*provider.SQLResult, error) {
	f.execCnt++
	return &provider.SQLResult{}, f.execErr
}
func (f *fakeDBProvider) BackupNow(context.Context, provider.DatabaseRequest) (*provider.BackupRef, error) {
	return nil, nil
}
func (f *fakeDBProvider) ListBackups(context.Context, provider.DatabaseRequest) ([]provider.BackupRef, error) {
	return nil, nil
}
func (f *fakeDBProvider) DownloadBackup(context.Context, provider.DatabaseRequest, string, io.Writer) error {
	return nil
}
func (f *fakeDBProvider) Restore(context.Context, provider.DatabaseRequest, string) error {
	return nil
}
func (f *fakeDBProvider) ListResources(context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}
func (f *fakeDBProvider) Close() error { return nil }

// TestDescribeDatabases_NotReconciled_NoSecret locks the read-only path
// for the "deploy hasn't run yet" case: credentials Secret missing →
// State=Not reconciled, Live=—, no probe call (ExecSQL not invoked).
func TestDescribeDatabases_NotReconciled_NoSecret(t *testing.T) {
	ns := "nvoi-myapp-prod"
	fp := &fakeDBProvider{}
	probe := DatabaseProbe{
		Name:     "main",
		Engine:   "postgres",
		Provider: fp,
		Request: provider.DatabaseRequest{
			CredentialsSecretName: "nvoi-myapp-prod-db-main-credentials",
			PodName:               "nvoi-myapp-prod-db-main-0",
		},
	}
	kc := newKC()

	rows := describeDatabases(context.Background(), kc, ns, []DatabaseProbe{probe})
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].State != "Not reconciled" {
		t.Errorf("State = %q, want Not reconciled", rows[0].State)
	}
	if rows[0].Live != "—" {
		t.Errorf("Live = %q, want —", rows[0].Live)
	}
	if fp.execCnt != 0 {
		t.Errorf("probe ran on Not reconciled DB (ExecSQL called %d times)", fp.execCnt)
	}
}

// TestDescribeDatabases_Selfhosted_Up locks the happy path for postgres:
// Secret + StatefulSet both present, ExecSQL succeeds → State=Ready 1/1,
// Live=Up, Endpoint shows the in-cluster Service host:port.
func TestDescribeDatabases_Selfhosted_Up(t *testing.T) {
	ns := "nvoi-myapp-prod"
	credsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nvoi-myapp-prod-db-main-credentials", Namespace: ns,
		},
		Data: map[string][]byte{
			"url":  []byte("postgres://u:p@nvoi-myapp-prod-db-main:5432/myapp"),
			"host": []byte("nvoi-myapp-prod-db-main"),
			"port": []byte("5432"),
		},
	}
	one := int32(1)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "nvoi-myapp-prod-db-main", Namespace: ns},
		Spec:       appsv1.StatefulSetSpec{Replicas: &one},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1},
	}
	kc := newKC(credsSecret, sts)
	fp := &fakeDBProvider{}
	probe := DatabaseProbe{
		Name:     "main",
		Engine:   "postgres",
		Provider: fp,
		Request: provider.DatabaseRequest{
			CredentialsSecretName: "nvoi-myapp-prod-db-main-credentials",
			PodName:               "nvoi-myapp-prod-db-main-0",
		},
	}

	rows := describeDatabases(context.Background(), kc, ns, []DatabaseProbe{probe})
	if rows[0].State != "Ready 1/1" {
		t.Errorf("State = %q, want Ready 1/1", rows[0].State)
	}
	if rows[0].Live != "Up" {
		t.Errorf("Live = %q, want Up", rows[0].Live)
	}
	if rows[0].Endpoint != "nvoi-myapp-prod-db-main:5432" {
		t.Errorf("Endpoint = %q", rows[0].Endpoint)
	}
	if fp.execCnt != 1 {
		t.Errorf("ExecSQL called %d times, want 1", fp.execCnt)
	}
}

// TestDescribeDatabases_SaaS_Down locks the SaaS path: no PodName (so
// no StatefulSet lookup), Secret present → State=Ready, but probe
// returns an error → Live="Down: <reason>".
func TestDescribeDatabases_SaaS_Down(t *testing.T) {
	ns := "nvoi-myapp-prod"
	credsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nvoi-myapp-prod-db-events-credentials", Namespace: ns,
		},
		Data: map[string][]byte{
			"url":  []byte("postgres://u:p@ep-foo.us-east-1.aws.neon.tech:5432/events?sslmode=require"),
			"host": []byte("ep-foo.us-east-1.aws.neon.tech"),
			"port": []byte("5432"),
		},
	}
	kc := newKC(credsSecret)
	fp := &fakeDBProvider{execErr: errors.New("connection refused")}
	probe := DatabaseProbe{
		Name:     "events",
		Engine:   "neon",
		Provider: fp,
		Request: provider.DatabaseRequest{
			CredentialsSecretName: "nvoi-myapp-prod-db-events-credentials",
			// No PodName — SaaS engine.
		},
	}

	rows := describeDatabases(context.Background(), kc, ns, []DatabaseProbe{probe})
	if rows[0].State != "Ready" {
		t.Errorf("State = %q, want Ready", rows[0].State)
	}
	if !strings.Contains(rows[0].Live, "Down") {
		t.Errorf("Live = %q, want Down: <reason>", rows[0].Live)
	}
	if !strings.Contains(rows[0].Live, "connection refused") {
		t.Errorf("Live = %q, expected to surface 'connection refused'", rows[0].Live)
	}
	if rows[0].Engine != "neon" {
		t.Errorf("Engine = %q, want neon", rows[0].Engine)
	}
}

// TestDescribeDatabases_ProbeRunsInParallel locks the parallel-probe
// contract: with 3 probes that each block 200ms, total wall time is
// ~200ms (parallel), not ~600ms (sequential). 500ms upper bound gives
// generous slack for CI variance.
func TestDescribeDatabases_ProbeRunsInParallel(t *testing.T) {
	ns := "ns"
	cs := k8sfake.NewSimpleClientset()
	for i := 0; i < 3; i++ {
		_, err := cs.CoreV1().Secrets(ns).Create(context.Background(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("creds-%d", i), Namespace: ns},
			Data:       map[string][]byte{"url": []byte("postgres://u@host/db")},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}
	kc := kube.NewForTest(cs)

	probes := make([]DatabaseProbe, 3)
	for i := range probes {
		probes[i] = DatabaseProbe{
			Name:     fmt.Sprintf("db%d", i),
			Engine:   "neon",
			Provider: &slowProvider{delay: 200 * time.Millisecond},
			Request: provider.DatabaseRequest{
				CredentialsSecretName: fmt.Sprintf("creds-%d", i),
			},
		}
	}

	start := time.Now()
	describeDatabases(context.Background(), kc, ns, probes)
	elapsed := time.Since(start)

	// Sequential would be ~600ms. Parallel should be ~200ms. Generous
	// upper bound for CI noise.
	if elapsed > 500*time.Millisecond {
		t.Errorf("probes ran sequentially: %v (parallel would be ~200ms)", elapsed)
	}
}

type slowProvider struct {
	fakeDBProvider
	delay time.Duration
}

func (s *slowProvider) ExecSQL(ctx context.Context, _ provider.DatabaseRequest, _ string) (*provider.SQLResult, error) {
	select {
	case <-time.After(s.delay):
		return &provider.SQLResult{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ── SECRETS section (namespace-wide, owner-classified) ──────────────────────

// TestDescribeSecrets_ListsAllNvoiManaged locks the new SECRETS section
// shape: every Secret in the namespace carrying NvoiSelector appears as
// one row with all its keys (sorted), classified by name pattern.
//
// Covers each owner classification we shipped:
//   - service:X (workload secret matching a cfg.Services entry)
//   - cron:X    (workload secret matching a cfg.Crons entry)
//   - workload:X (orphan — no cfg match, lingering from a previous deploy)
//   - database:X       (per-DB credentials Secret)
//   - database:X:bk    (per-DB backup-creds Secret)
//   - registry         (kubernetes.io/dockerconfigjson)
//   - tunnel:cloudflare / tunnel:ngrok (agent auth Secrets)
func TestDescribeSecrets_ListsAllNvoiManaged(t *testing.T) {
	ns := "nvoi-myapp-prod"
	mkSecret := func(name string, keys ...string) *corev1.Secret {
		data := map[string][]byte{}
		for _, k := range keys {
			data[k] = []byte("x")
		}
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Labels:    managedLabels(),
			},
			Data: data,
		}
	}
	objs := []runtime.Object{
		mkSecret("api-secrets", "DATABASE_URL"),
		mkSecret("backfill-secrets", "API_TOKEN"),
		mkSecret("orphan-secrets", "OLD_KEY"),
		mkSecret("nvoi-myapp-prod-db-main-credentials", "url", "user", "password", "host", "port", "database", "sslmode"),
		mkSecret("nvoi-myapp-prod-db-main-backup-creds", "BUCKET_ENDPOINT", "BUCKET_NAME", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION", "ENGINE", "DATABASE_URL"),
		mkSecret("registry-auth", ".dockerconfigjson"),
		mkSecret("cloudflared-token", "token"),
		// Unlabeled Secret must NOT appear.
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "user-app-secret", Namespace: ns},
			Data:       map[string][]byte{"value": []byte("x")},
		},
	}
	kc := newKC(objs...)
	names, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	rows := describeSecrets(context.Background(), kc, ns, names,
		[]string{"api"},      // services in cfg
		[]string{"backfill"}, // crons in cfg
	)

	want := map[string]string{
		"api-secrets":                          "service:api",
		"backfill-secrets":                     "cron:backfill",
		"orphan-secrets":                       "workload:orphan",
		"nvoi-myapp-prod-db-main-credentials":  "database:main",
		"nvoi-myapp-prod-db-main-backup-creds": "database:main:bk",
		"registry-auth":                        "registry",
		"cloudflared-token":                    "tunnel:cloudflare",
	}
	if len(rows) != len(want) {
		var got []string
		for _, r := range rows {
			got = append(got, r.Name)
		}
		t.Fatalf("rows = %v, want %d", got, len(want))
	}
	for _, r := range rows {
		owner, ok := want[r.Name]
		if !ok {
			t.Errorf("unexpected Secret in output: %q", r.Name)
			continue
		}
		if r.Owner != owner {
			t.Errorf("%s: Owner = %q, want %q", r.Name, r.Owner, owner)
		}
		if len(r.Keys) == 0 {
			t.Errorf("%s: Keys empty, want at least one", r.Name)
		}
	}

	// Keys are sorted within each Secret — locks the deterministic
	// rendering contract.
	for _, r := range rows {
		for i := 1; i < len(r.Keys); i++ {
			if r.Keys[i-1] > r.Keys[i] {
				t.Errorf("%s: Keys not sorted: %v", r.Name, r.Keys)
				break
			}
		}
	}
}

// TestDescribeSecrets_EmptyNamespace covers the common "no nvoi-managed
// Secrets in the namespace" case (e.g. before first deploy). Should
// return nil cleanly, no panic, no error.
func TestDescribeSecrets_EmptyNamespace(t *testing.T) {
	ns := "nvoi-empty"
	kc := newKC()
	names, _ := utils.NewNames("empty", "prod")
	rows := describeSecrets(context.Background(), kc, ns, names, nil, nil)
	if rows != nil {
		t.Errorf("rows = %v, want nil", rows)
	}
}

func TestClassifySecretOwner_Patterns(t *testing.T) {
	base := "nvoi-myapp-prod"
	services := map[string]bool{"api": true}
	crons := map[string]bool{"cleanup": true}
	cases := map[string]string{
		"api-secrets":                          "service:api",
		"cleanup-secrets":                      "cron:cleanup",
		"foo-secrets":                          "workload:foo", // orphan
		"nvoi-myapp-prod-db-main-credentials":  "database:main",
		"nvoi-myapp-prod-db-main-backup-creds": "database:main:bk",
		"registry-auth":                        "registry",
		"cloudflared-token":                    "tunnel:cloudflare",
		"ngrok-authtoken":                      "tunnel:ngrok",
		"some-other-secret":                    "", // unknown — empty owner
	}
	for name, want := range cases {
		if got := classifySecretOwner(name, base, services, crons); got != want {
			t.Errorf("classifySecretOwner(%q) = %q, want %q", name, got, want)
		}
	}
}
