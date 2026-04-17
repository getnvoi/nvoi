package reconcile

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/config"
)

const testNS = "nvoi-myapp-prod"

func getSecret(t *testing.T, dc *config.DeployContext, name string) *corev1.Secret {
	t.Helper()
	sec, err := kfFor(dc).Typed.CoreV1().Secrets(testNS).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret %q: %v", name, err)
	}
	return sec
}

func getDeployment(t *testing.T, dc *config.DeployContext, name string) *appsv1.Deployment {
	t.Helper()
	dep, err := kfFor(dc).Typed.AppsV1().Deployments(testNS).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment %q: %v", name, err)
	}
	return dep
}

func TestServices_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}

	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !kfFor(dc).HasDeployment(testNS, "web") {
		t.Error("web deployment not applied")
	}
	if !kfFor(dc).HasService(testNS, "web") {
		t.Error("web service not applied")
	}
}

func TestServices_OrphanRemoved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)

	// Pre-populate the fake with the orphan so Services can find it and delete.
	kf := kfFor(dc)
	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "old-api", Namespace: testNS},
	}
	if _, err := kf.Typed.AppsV1().Deployments(testNS).Create(context.Background(), existing, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	live := &config.LiveState{Services: []string{"web", "old-api"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kf.HasDeployment(testNS, "old-api") {
		t.Error("orphan old-api should have been deleted")
	}
	if !kf.HasDeployment(testNS, "web") {
		t.Error("desired web deployment should exist")
	}
}

func TestServices_AlreadyConverged(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	kf := kfFor(dc)

	// Pre-populate the fake with the desired resource.
	one := int32(1)
	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: testNS},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	}
	if _, err := kf.Typed.AppsV1().Deployments(testNS).Create(context.Background(), existing, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	live := &config.LiveState{Services: []string{"web"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Still present — not nuked.
	if !kf.HasDeployment(testNS, "web") {
		t.Error("converged web should not be deleted")
	}
}

func TestServices_CompleteReplacement(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	kf := kfFor(dc)

	// Pre-populate orphans.
	for _, name := range []string{"old-web", "old-worker"} {
		_, err := kf.Typed.AppsV1().Deployments(testNS).Create(context.Background(),
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS}},
			metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"new-api": {Image: "api:v2", Port: 8080}},
	}
	live := &config.LiveState{Services: []string{"old-web", "old-worker"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, name := range []string{"old-web", "old-worker"} {
		if kf.HasDeployment(testNS, name) {
			t.Errorf("orphan %q should have been deleted", name)
		}
	}
	if !kf.HasDeployment(testNS, "new-api") {
		t.Error("desired new-api not applied")
	}
}

func TestServices_EveryServiceGetsRolloutWait(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"api":    {Image: "api:v1", Port: 8080},
			"web":    {Image: "nginx", Port: 80},
			"worker": {Image: "worker:v1", Port: 9090},
		},
	}

	// AutoReadyPods satisfies WaitRollout for every service.
	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All three deployments must exist — only way all three WaitRollouts
	// returned nil is if each one saw a ready pod.
	for _, name := range []string{"api", "web", "worker"} {
		if !kfFor(dc).HasDeployment(testNS, name) {
			t.Errorf("%s not applied", name)
		}
	}
}

func TestServices_PerServiceSecretCreated(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Secrets: []string{"WEB_SECRET"}},
		},
	}
	sources := map[string]string{"WEB_SECRET": "s3cret"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec := getSecret(t, dc, "web-secrets")
	if string(sec.Data["WEB_SECRET"]) != "s3cret" {
		t.Errorf("WEB_SECRET = %q, want s3cret", string(sec.Data["WEB_SECRET"]))
	}
}

func TestServices_PerServiceSecretWithDollarVar(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Secrets: []string{"DATABASE_URL=$MAIN_DATABASE_URL"}},
		},
	}
	sources := map[string]string{"MAIN_DATABASE_URL": "postgresql://host/db"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec := getSecret(t, dc, "web-secrets")
	if string(sec.Data["DATABASE_URL"]) != "postgresql://host/db" {
		t.Errorf("DATABASE_URL = %q", string(sec.Data["DATABASE_URL"]))
	}
}

func TestServices_PerServiceSecretComposed(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Secrets: []string{"CREATE_SUPERUSER=$DB_USER:$DB_PASS"}},
		},
	}
	sources := map[string]string{"DB_USER": "admin", "DB_PASS": "secret"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec := getSecret(t, dc, "web-secrets")
	if string(sec.Data["CREATE_SUPERUSER"]) != "admin:secret" {
		t.Errorf("composed = %q", string(sec.Data["CREATE_SUPERUSER"]))
	}
}

func TestServices_PerServiceSecretAliasedWithDollar(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Secrets: []string{"SECRET_KEY=$BUGSINK_SECRET_KEY"}},
		},
	}
	sources := map[string]string{"BUGSINK_SECRET_KEY": "keyval"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec := getSecret(t, dc, "web-secrets")
	if string(sec.Data["SECRET_KEY"]) != "keyval" {
		t.Errorf("aliased = %q", string(sec.Data["SECRET_KEY"]))
	}
}

func TestServices_EnvWithDollarResolved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Env: []string{"BASE_URL=https://$HOST/api"}},
		},
	}
	sources := map[string]string{"HOST": "example.com"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dep := getDeployment(t, dc, "web")
	var found bool
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "BASE_URL" && e.Value == "https://example.com/api" {
			found = true
		}
	}
	if !found {
		t.Errorf("resolved env missing: %+v", dep.Spec.Template.Spec.Containers[0].Env)
	}
}

func TestServices_EnvLiteral(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Env: []string{"FOO=bar"}},
		},
	}

	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dep := getDeployment(t, dc, "web")
	var found bool
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "FOO" && e.Value == "bar" {
			found = true
		}
	}
	if !found {
		t.Errorf("literal env missing: %+v", dep.Spec.Template.Spec.Containers[0].Env)
	}
}

func TestServices_NoSecretsDeletesPerServiceSecret(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	kf := kfFor(dc)

	// Pre-populate a stale per-service secret.
	_, err := kf.Typed.CoreV1().Secrets(testNS).Create(context.Background(),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "web-secrets", Namespace: testNS},
			Data:       map[string][]byte{"OLD": []byte("x")},
		}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}

	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kf.HasSecret(testNS, "web-secrets") {
		t.Error("web-secrets should have been deleted when service declares no secrets")
	}
}

func TestServices_OrphanServiceDeletesItsSecret(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	kf := kfFor(dc)

	// Pre-populate orphan + its secret.
	_, _ = kf.Typed.AppsV1().Deployments(testNS).Create(context.Background(),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "old-api", Namespace: testNS}},
		metav1.CreateOptions{})
	_, _ = kf.Typed.CoreV1().Secrets(testNS).Create(context.Background(),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "old-api-secrets", Namespace: testNS}},
		metav1.CreateOptions{})

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	live := &config.LiveState{Services: []string{"web", "old-api"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kf.HasSecret(testNS, "old-api-secrets") {
		t.Error("orphan old-api-secrets not cleaned up")
	}
}

func TestServices_StorageCredsInPerServiceSecret(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Storage:  map[string]config.StorageDef{"assets": {}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80, Storage: []string{"assets"}}},
	}
	sources := map[string]string{
		"STORAGE_ASSETS_ENDPOINT":          "https://s3.example.com",
		"STORAGE_ASSETS_BUCKET":            "nvoi-myapp-prod-assets",
		"STORAGE_ASSETS_ACCESS_KEY_ID":     "AKID",
		"STORAGE_ASSETS_SECRET_ACCESS_KEY": "SECRET",
	}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec := getSecret(t, dc, "web-secrets")
	if string(sec.Data["STORAGE_ASSETS_ACCESS_KEY_ID"]) != "AKID" {
		t.Errorf("access key missing: %q", string(sec.Data["STORAGE_ASSETS_ACCESS_KEY_ID"]))
	}
	if string(sec.Data["STORAGE_ASSETS_SECRET_ACCESS_KEY"]) != "SECRET" {
		t.Errorf("secret key missing: %q", string(sec.Data["STORAGE_ASSETS_SECRET_ACCESS_KEY"]))
	}
}

func TestServices_NoAutoInjection(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	sources := map[string]string{"MAIN_DATABASE_URL": "postgresql://host/db"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No per-service secret because no secrets declared; no env ref either.
	if kfFor(dc).HasSecret(testNS, "web-secrets") {
		t.Error("web-secrets should not exist when no secrets declared")
	}
}
