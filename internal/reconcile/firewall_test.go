package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestFirewall_Applied(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &config.AppConfig{Firewall: []string{"default"}}

	if err := Firewall(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("firewall:" + n.Firewall()) {
		t.Errorf("firewall not applied: %v", log.all())
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
	cfg := &config.AppConfig{Firewall: []string{"default"}}

	_ = Firewall(context.Background(), dc, nil, cfg)
	_ = Firewall(context.Background(), dc, nil, cfg)

	if log.count("firewall:"+n.Firewall()) != 2 {
		t.Errorf("expected 2 calls (idempotent): %v", log.all())
	}
}
