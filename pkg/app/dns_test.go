package app

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/kube"
)

func TestMergeRouteReplacesExisting(t *testing.T) {
	existing := []kube.IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"old.com"}},
		{Service: "api", Port: 8080, Domains: []string{"api.com"}},
	}
	updated := mergeRoute(existing, kube.IngressRoute{
		Service: "web", Port: 3000, Domains: []string{"new.com"},
	})
	if len(updated) != 2 {
		t.Fatalf("replace existing: got %d routes, want 2", len(updated))
	}
	if updated[0].Domains[0] != "new.com" {
		t.Errorf("replace existing: web domains = %v, want [new.com]", updated[0].Domains)
	}
}

func TestMergeRouteAppendsNew(t *testing.T) {
	existing := []kube.IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"web.com"}},
	}
	updated := mergeRoute(existing, kube.IngressRoute{
		Service: "api", Port: 8080, Domains: []string{"api.com"},
	})
	if len(updated) != 2 {
		t.Fatalf("append new: got %d routes, want 2", len(updated))
	}
	if updated[1].Service != "api" {
		t.Errorf("append new: second route service = %q, want %q", updated[1].Service, "api")
	}
}

func TestMergeRouteNilInput(t *testing.T) {
	updated := mergeRoute(nil, kube.IngressRoute{
		Service: "web", Port: 3000, Domains: []string{"web.com"},
	})
	if len(updated) != 1 {
		t.Fatalf("nil input: got %d routes, want 1", len(updated))
	}
	if updated[0].Service != "web" {
		t.Errorf("nil input: route service = %q, want %q", updated[0].Service, "web")
	}
}

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
