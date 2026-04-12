package kube

import (
	"encoding/json"
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	sigsyaml "sigs.k8s.io/yaml"
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

func TestKubeIngressName(t *testing.T) {
	if got := KubeIngressName("web"); got != "ingress-web" {
		t.Errorf("got %q, want ingress-web", got)
	}
	if got := KubeIngressName("api"); got != "ingress-api" {
		t.Errorf("got %q, want ingress-api", got)
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
