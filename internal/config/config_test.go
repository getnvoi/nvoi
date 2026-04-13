package config

import (
	"testing"
)

func TestDatabaseNames(t *testing.T) {
	cfg := &AppConfig{
		Database: map[string]DatabaseDef{
			"main":      {Image: "postgres:17", Volume: "pgdata"},
			"analytics": {Image: "postgres:17", Volume: "analytics-data"},
		},
	}
	names := cfg.DatabaseNames()
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
	if names[0] != "analytics" || names[1] != "main" {
		t.Fatalf("names = %v, want [analytics main]", names)
	}
}

func TestDatabaseNames_Empty(t *testing.T) {
	cfg := &AppConfig{}
	names := cfg.DatabaseNames()
	if len(names) != 0 {
		t.Fatalf("len = %d, want 0", len(names))
	}
}

func TestDatabaseNames_Nil(t *testing.T) {
	var cfg *AppConfig
	names := cfg.DatabaseNames()
	if names != nil {
		t.Fatalf("got %v, want nil", names)
	}
}
