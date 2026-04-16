package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestFirewall_BothRolesApplied(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &config.AppConfig{App: "myapp", Env: "prod", Firewall: []string{"default"},
		MasterFirewall: "nvoi-myapp-prod-master-fw", WorkerFirewall: "nvoi-myapp-prod-worker-fw"}

	if err := Firewall(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("firewall:" + n.MasterFirewall()) {
		t.Errorf("master firewall not applied: %v", log.all())
	}
	if !log.has("firewall:" + n.WorkerFirewall()) {
		t.Errorf("worker firewall not applied: %v", log.all())
	}
}

func TestFirewall_EmptySkipped(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	cfg := &config.AppConfig{}

	if err := Firewall(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if log.count("firewall:") != 0 {
		t.Errorf("empty firewall should skip: %v", log.all())
	}
}

func TestFirewall_Idempotent(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &config.AppConfig{App: "myapp", Env: "prod", Firewall: []string{"default"},
		MasterFirewall: "nvoi-myapp-prod-master-fw", WorkerFirewall: "nvoi-myapp-prod-worker-fw"}

	_ = Firewall(context.Background(), dc, nil, cfg)
	_ = Firewall(context.Background(), dc, nil, cfg)

	if log.count("firewall:"+n.MasterFirewall()) != 2 {
		t.Errorf("expected 2 master firewall calls: %v", log.all())
	}
	if log.count("firewall:"+n.WorkerFirewall()) != 2 {
		t.Errorf("expected 2 worker firewall calls: %v", log.all())
	}
}
