package core

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/kube"
)

func TestRemoveRouteMatching(t *testing.T) {
	routes := []kube.IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"web.com"}},
		{Service: "api", Port: 8080, Domains: []string{"api.com"}},
	}
	result := removeRoute(routes, "web")
	if len(result) != 1 {
		t.Fatalf("remove matching: got %d routes, want 1", len(result))
	}
	if result[0].Service != "api" {
		t.Errorf("remove matching: remaining service = %q, want %q", result[0].Service, "api")
	}
}

func TestRemoveRouteLastReturnsNil(t *testing.T) {
	routes := []kube.IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"web.com"}},
	}
	result := removeRoute(routes, "web")
	if result != nil {
		t.Errorf("remove last: got %v, want nil", result)
	}
}

func TestRemoveRouteNotFound(t *testing.T) {
	routes := []kube.IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"web.com"}},
	}
	result := removeRoute(routes, "api")
	if len(result) != 1 {
		t.Fatalf("not found: got %d routes, want 1", len(result))
	}
	if result[0].Service != "web" {
		t.Errorf("not found: route service = %q, want %q", result[0].Service, "web")
	}
}

func TestParseIngressArgs(t *testing.T) {
	routes, err := ParseIngressArgs([]string{
		"web:example.com,www.example.com",
		"api:api.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(routes))
	}
	if routes[0].Service != "web" {
		t.Errorf("route[0] service = %q, want web", routes[0].Service)
	}
	if len(routes[0].Domains) != 2 {
		t.Errorf("route[0] domains = %v, want 2", routes[0].Domains)
	}
	if routes[1].Service != "api" {
		t.Errorf("route[1] service = %q, want api", routes[1].Service)
	}
}

func TestParseIngressArgs_Invalid(t *testing.T) {
	_, err := ParseIngressArgs([]string{"nodomain"})
	if err == nil {
		t.Fatal("expected error for missing colon")
	}
	_, err = ParseIngressArgs([]string{"svc:"})
	if err == nil {
		t.Fatal("expected error for empty domains")
	}
}
