package kube

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/internal/testutil"
)

func TestGenerateIngressYAML_SingleDomain_ACME(t *testing.T) {
	route := IngressRoute{Service: "web", Port: 3000, Domains: []string{"example.com"}}
	yaml, err := GenerateIngressYAML(route, "nvoi-test-prod", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ingress networkingv1.Ingress
	if err := sigsyaml.Unmarshal([]byte(yaml), &ingress); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}

	if ingress.Name != "ingress-web" {
		t.Errorf("name = %q, want ingress-web", ingress.Name)
	}
	if ingress.Namespace != "nvoi-test-prod" {
		t.Errorf("namespace = %q, want nvoi-test-prod", ingress.Namespace)
	}

	// Annotations
	if ingress.Annotations["traefik.ingress.kubernetes.io/router.tls.certresolver"] != "letsencrypt" {
		t.Error("missing certresolver annotation")
	}
	if ingress.Annotations["traefik.ingress.kubernetes.io/router.entrypoints"] != "web,websecure" {
		t.Error("missing entrypoints annotation")
	}

	// Rules
	if len(ingress.Spec.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(ingress.Spec.Rules))
	}
	rule := ingress.Spec.Rules[0]
	if rule.Host != "example.com" {
		t.Errorf("host = %q, want example.com", rule.Host)
	}
	if rule.HTTP.Paths[0].Backend.Service.Name != "web" {
		t.Errorf("backend service = %q, want web", rule.HTTP.Paths[0].Backend.Service.Name)
	}
	if rule.HTTP.Paths[0].Backend.Service.Port.Number != 3000 {
		t.Errorf("backend port = %d, want 3000", rule.HTTP.Paths[0].Backend.Service.Port.Number)
	}

	// TLS
	if len(ingress.Spec.TLS) != 1 {
		t.Fatalf("tls = %d, want 1", len(ingress.Spec.TLS))
	}
	if ingress.Spec.TLS[0].SecretName != "tls-web" {
		t.Errorf("tls secret = %q, want tls-web", ingress.Spec.TLS[0].SecretName)
	}
	if ingress.Spec.TLS[0].Hosts[0] != "example.com" {
		t.Errorf("tls host = %q, want example.com", ingress.Spec.TLS[0].Hosts[0])
	}
}

func TestGenerateIngressYAML_MultipleDomains(t *testing.T) {
	route := IngressRoute{Service: "web", Port: 3000, Domains: []string{"example.com", "www.example.com"}}
	yaml, err := GenerateIngressYAML(route, "ns", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ingress networkingv1.Ingress
	if err := sigsyaml.Unmarshal([]byte(yaml), &ingress); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}

	if len(ingress.Spec.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(ingress.Spec.Rules))
	}
	if ingress.Spec.Rules[0].Host != "example.com" {
		t.Errorf("rule[0] host = %q", ingress.Spec.Rules[0].Host)
	}
	if ingress.Spec.Rules[1].Host != "www.example.com" {
		t.Errorf("rule[1] host = %q", ingress.Spec.Rules[1].Host)
	}

	if len(ingress.Spec.TLS) != 1 {
		t.Fatalf("tls = %d, want 1", len(ingress.Spec.TLS))
	}
	if len(ingress.Spec.TLS[0].Hosts) != 2 {
		t.Fatalf("tls hosts = %d, want 2", len(ingress.Spec.TLS[0].Hosts))
	}
}

func TestGenerateIngressYAML_NoACME(t *testing.T) {
	route := IngressRoute{Service: "web", Port: 3000, Domains: []string{"example.com"}}
	yaml, err := GenerateIngressYAML(route, "ns", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ingress networkingv1.Ingress
	if err := sigsyaml.Unmarshal([]byte(yaml), &ingress); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}

	// No certresolver annotation
	if _, ok := ingress.Annotations["traefik.ingress.kubernetes.io/router.tls.certresolver"]; ok {
		t.Error("certresolver annotation should not be present when ACME is false")
	}

	// Entrypoints still set
	if ingress.Annotations["traefik.ingress.kubernetes.io/router.entrypoints"] != "web,websecure" {
		t.Error("entrypoints annotation should always be present")
	}

	// No TLS block
	if len(ingress.Spec.TLS) != 0 {
		t.Errorf("tls should be empty when ACME is false, got %d", len(ingress.Spec.TLS))
	}
}

func TestGenerateIngressYAML_ValidYAML(t *testing.T) {
	route := IngressRoute{Service: "api", Port: 8080, Domains: []string{"api.example.com"}}
	yaml, err := GenerateIngressYAML(route, "ns", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must be valid YAML that parses to valid JSON
	var raw map[string]any
	if err := sigsyaml.Unmarshal([]byte(yaml), &raw); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	if raw["apiVersion"] != "networking.k8s.io/v1" {
		t.Errorf("apiVersion = %v, want networking.k8s.io/v1", raw["apiVersion"])
	}
	if raw["kind"] != "Ingress" {
		t.Errorf("kind = %v, want Ingress", raw["kind"])
	}
}

func TestGetIngressRoutes_ParsesJSON(t *testing.T) {
	// Simulate kubectl JSON output
	list := networkingv1.IngressList{
		Items: []networkingv1.Ingress{
			{
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{
						{
							Host: "example.com",
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{{
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: "web",
												Port: networkingv1.ServiceBackendPort{Number: 3000},
											},
										},
									}},
								},
							},
						},
						{
							Host: "www.example.com",
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{{
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: "web",
												Port: networkingv1.ServiceBackendPort{Number: 3000},
											},
										},
									}},
								},
							},
						},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(list)

	// Parse using the same logic as GetIngressRoutes
	var parsed networkingv1.IngressList
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	byService := map[string]*IngressRoute{}
	for _, item := range parsed.Items {
		for _, rule := range item.Spec.Rules {
			if rule.HTTP == nil || len(rule.HTTP.Paths) == 0 {
				continue
			}
			path := rule.HTTP.Paths[0]
			if path.Backend.Service == nil {
				continue
			}
			svc := path.Backend.Service.Name
			port := int(path.Backend.Service.Port.Number)
			if _, ok := byService[svc]; !ok {
				byService[svc] = &IngressRoute{Service: svc, Port: port}
			}
			byService[svc].Domains = append(byService[svc].Domains, rule.Host)
		}
	}

	if len(byService) != 1 {
		t.Fatalf("expected 1 service, got %d", len(byService))
	}
	web := byService["web"]
	if web.Port != 3000 {
		t.Errorf("port = %d, want 3000", web.Port)
	}
	if len(web.Domains) != 2 {
		t.Fatalf("domains = %d, want 2", len(web.Domains))
	}
}

// ingressRule builds a single IngressRule pointing to service:port.
func ingressRule(host, service string, port int) networkingv1.IngressRule {
	return networkingv1.IngressRule{
		Host: host,
		IngressRuleValue: networkingv1.IngressRuleValue{
			HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Backend: networkingv1.IngressBackend{
						Service: &networkingv1.IngressServiceBackend{
							Name: service,
							Port: networkingv1.ServiceBackendPort{Number: int32(port)},
						},
					},
				}},
			},
		},
	}
}

// ingressListSSH returns a MockSSH that responds to "get ingress" with the serialized list.
func ingressListSSH(items ...networkingv1.Ingress) *testutil.MockSSH {
	list := networkingv1.IngressList{Items: items}
	data, _ := json.Marshal(list)
	return &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get ingress", Result: testutil.MockResult{Output: data}},
		},
	}
}

func findRoute(routes []IngressRoute, service string) *IngressRoute {
	for _, r := range routes {
		if r.Service == service {
			return &r
		}
	}
	return nil
}

func TestGetIngressRoutes_TwoServices(t *testing.T) {
	ssh := ingressListSSH(
		networkingv1.Ingress{Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{ingressRule("example.com", "web", 3000)},
		}},
		networkingv1.Ingress{Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{ingressRule("api.example.com", "api", 8080)},
		}},
	)

	routes, err := GetIngressRoutes(context.Background(), ssh, "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	web := findRoute(routes, "web")
	api := findRoute(routes, "api")
	if web == nil {
		t.Fatal("missing route for web")
	}
	if api == nil {
		t.Fatal("missing route for api")
	}
	if web.Port != 3000 {
		t.Errorf("web port = %d, want 3000", web.Port)
	}
	if api.Port != 8080 {
		t.Errorf("api port = %d, want 8080", api.Port)
	}
	if len(web.Domains) != 1 || web.Domains[0] != "example.com" {
		t.Errorf("web domains = %v, want [example.com]", web.Domains)
	}
	if len(api.Domains) != 1 || api.Domains[0] != "api.example.com" {
		t.Errorf("api domains = %v, want [api.example.com]", api.Domains)
	}
}

func TestGetIngressRoutes_SameServiceTwoIngresses_DomainsMerge(t *testing.T) {
	// Two separate Ingress resources both pointing to "web" — domains should merge.
	ssh := ingressListSSH(
		networkingv1.Ingress{Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{ingressRule("example.com", "web", 3000)},
		}},
		networkingv1.Ingress{Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{ingressRule("www.example.com", "web", 3000)},
		}},
	)

	routes, err := GetIngressRoutes(context.Background(), ssh, "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route (merged), got %d", len(routes))
	}
	if routes[0].Service != "web" {
		t.Errorf("service = %q, want web", routes[0].Service)
	}
	if len(routes[0].Domains) != 2 {
		t.Fatalf("expected 2 domains merged, got %d: %v", len(routes[0].Domains), routes[0].Domains)
	}
}

func TestGetIngressRoutes_MultiRulesPerIngress(t *testing.T) {
	// One Ingress with multiple rules — same service, multiple domains.
	ssh := ingressListSSH(
		networkingv1.Ingress{Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				ingressRule("example.com", "web", 3000),
				ingressRule("www.example.com", "web", 3000),
				ingressRule("api.example.com", "api", 8080),
			},
		}},
	)

	routes, err := GetIngressRoutes(context.Background(), ssh, "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	web := findRoute(routes, "web")
	api := findRoute(routes, "api")
	if web == nil || api == nil {
		t.Fatalf("missing routes: web=%v api=%v", web, api)
	}
	if len(web.Domains) != 2 {
		t.Errorf("web should have 2 domains, got %d: %v", len(web.Domains), web.Domains)
	}
	if len(api.Domains) != 1 {
		t.Errorf("api should have 1 domain, got %d: %v", len(api.Domains), api.Domains)
	}
}

func TestGetIngressRoutes_EmptyList(t *testing.T) {
	ssh := ingressListSSH() // no items

	routes, err := GetIngressRoutes(context.Background(), ssh, "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(routes))
	}
}

func TestGetIngressRoutes_SkipsRulesWithoutHTTP(t *testing.T) {
	// Rule with no HTTP block — should be skipped without error.
	ssh := ingressListSSH(
		networkingv1.Ingress{Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{Host: "tcp.example.com"}, // no HTTP
				ingressRule("example.com", "web", 3000),
			},
		}},
	)

	routes, err := GetIngressRoutes(context.Background(), ssh, "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if len(routes[0].Domains) != 1 || routes[0].Domains[0] != "example.com" {
		t.Errorf("domains = %v, want [example.com]", routes[0].Domains)
	}
}

func TestKubeIngressName(t *testing.T) {
	if got := KubeIngressName("web"); got != "ingress-web" {
		t.Errorf("got %q, want ingress-web", got)
	}
	if got := KubeIngressName("api"); got != "ingress-api" {
		t.Errorf("got %q, want ingress-api", got)
	}
}

func TestEnsureTraefikACME_UsesUploadNotHeredoc(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "apply --server-side", Result: testutil.MockResult{}},
			{Prefix: "get deploy traefik", Result: testutil.MockResult{Output: []byte("'1/1'")}},
		},
	}

	err := EnsureTraefikACME(context.Background(), mock, "admin@example.com", true)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Must use SFTP upload — not heredoc
	if len(mock.Uploads) != 1 {
		t.Fatalf("expected 1 upload (SFTP), got %d — heredoc bypasses upload", len(mock.Uploads))
	}
	yaml := string(mock.Uploads[0].Content)
	if !strings.Contains(yaml, "HelmChartConfig") {
		t.Error("uploaded YAML should contain HelmChartConfig")
	}
	if !strings.Contains(yaml, "admin@example.com") {
		t.Error("uploaded YAML should contain the ACME email")
	}
	if !strings.Contains(yaml, "letsencrypt") {
		t.Error("uploaded YAML should contain letsencrypt resolver config")
	}

	// Verify KUBECONFIG is used, no heredoc markers
	for _, cmd := range mock.Calls {
		if !strings.Contains(cmd, "KUBECONFIG=") {
			t.Errorf("should use KUBECONFIG env, got: %s", cmd)
		}
		if strings.Contains(cmd, "EOYAML") || strings.Contains(cmd, "cat <<") {
			t.Errorf("should not use heredoc, got: %s", cmd)
		}
	}
}

func TestEnsureTraefikACME_NoACME(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "apply --server-side", Result: testutil.MockResult{}},
			{Prefix: "get deploy traefik", Result: testutil.MockResult{Output: []byte("'1/1'")}},
		},
	}

	err := EnsureTraefikACME(context.Background(), mock, "", false)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(mock.Uploads) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(mock.Uploads))
	}
	yaml := string(mock.Uploads[0].Content)
	if !strings.Contains(yaml, "HelmChartConfig") {
		t.Error("uploaded YAML should contain HelmChartConfig")
	}
	// No ACME mode disables websecure
	if strings.Contains(yaml, "letsencrypt") {
		t.Error("no-ACME mode should not contain letsencrypt config")
	}
	if !strings.Contains(yaml, "websecure") {
		t.Error("no-ACME mode should disable websecure port")
	}
}

func TestEnsureTraefikACME_WaitsForTraefikReady(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "apply --server-side", Result: testutil.MockResult{}},
			{Prefix: "get deploy traefik", Result: testutil.MockResult{Output: []byte("'1/1'")}},
		},
	}

	err := EnsureTraefikACME(context.Background(), mock, "admin@example.com", true)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Must have called get deploy traefik to wait for readiness
	foundReadyCheck := false
	for _, cmd := range mock.Calls {
		if strings.Contains(cmd, "get deploy traefik") && strings.Contains(cmd, "jsonpath") {
			foundReadyCheck = true
		}
	}
	if !foundReadyCheck {
		t.Errorf("EnsureTraefikACME must wait for traefik readiness after applying config, calls: %v", mock.Calls)
	}
}

func TestGenerateIngressYAML_Labels(t *testing.T) {
	route := IngressRoute{Service: "web", Port: 3000, Domains: []string{"example.com"}}
	yaml, err := GenerateIngressYAML(route, "ns", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(yaml, "app.kubernetes.io/managed-by: nvoi") {
		t.Error("missing managed-by label")
	}
	if !strings.Contains(yaml, "app.kubernetes.io/name: ingress-web") {
		t.Error("missing app name label")
	}
}
