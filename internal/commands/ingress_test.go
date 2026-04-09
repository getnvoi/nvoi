package commands

import "testing"

func TestIngressSet_SingleRoute(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewIngressCmd(m), "set", "web:a.com")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "IngressSet")
	routes := m.last().Args[0].([]RouteArg)
	if len(routes) != 1 || routes[0].Service != "web" || routes[0].Domains[0] != "a.com" {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestIngressSet_MultiRoute(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewIngressCmd(m), "set", "web:a.com", "api:b.com")
	if err != nil {
		t.Fatal(err)
	}
	routes := m.last().Args[0].([]RouteArg)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	if routes[0].Service != "web" || routes[1].Service != "api" {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestIngressDelete_SingleRoute(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewIngressCmd(m), "delete", "web:a.com")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "IngressDelete")
	routes := m.last().Args[0].([]RouteArg)
	if len(routes) != 1 || routes[0].Service != "web" {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestIngressDelete_MultiRoute(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewIngressCmd(m), "delete", "web:a.com", "api:b.com")
	if err != nil {
		t.Fatal(err)
	}
	routes := m.last().Args[0].([]RouteArg)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
}
