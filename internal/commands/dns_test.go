package commands

import "testing"

func TestDNSSet_SingleRoute(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDNSCmd(m), "set", "web:example.com")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "DNSSet")
	routes := m.last().Args[0].([]RouteArg)
	if len(routes) != 1 || routes[0].Service != "web" || routes[0].Domains[0] != "example.com" {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestDNSSet_MultiRoute(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDNSCmd(m), "set", "web:a.com", "api:b.com")
	if err != nil {
		t.Fatal(err)
	}
	routes := m.last().Args[0].([]RouteArg)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
}

func TestDNSSet_MultiDomain(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDNSCmd(m), "set", "web:a.com,b.com")
	if err != nil {
		t.Fatal(err)
	}
	routes := m.last().Args[0].([]RouteArg)
	if len(routes[0].Domains) != 2 {
		t.Fatalf("domains = %v", routes[0].Domains)
	}
}

func TestDNSDelete_SingleRoute(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDNSCmd(m), "delete", "web:a.com")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "DNSDelete")
}

func TestDNSDelete_MissingArg(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDNSCmd(m), "delete")
	assertError(t, err, "")
}

func TestDNSList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDNSCmd(m), "list")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "DNSList")
}
