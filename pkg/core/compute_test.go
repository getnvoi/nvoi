package core

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestFindMaster_Found(t *testing.T) {
	ctx := context.Background()
	names, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	want := &provider.Server{
		ID:        "123",
		Name:      "nvoi-myapp-prod-master",
		Status:    provider.ServerRunning,
		IPv4:      "1.2.3.4",
		PrivateIP: "10.0.1.1",
	}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{want},
	}

	got, err := FindMaster(ctx, mock, names)
	if err != nil {
		t.Fatalf("FindMaster: unexpected error: %v", err)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.IPv4 != want.IPv4 {
		t.Errorf("IPv4 = %q, want %q", got.IPv4, want.IPv4)
	}
	if got.PrivateIP != want.PrivateIP {
		t.Errorf("PrivateIP = %q, want %q", got.PrivateIP, want.PrivateIP)
	}
}

func TestFindMaster_NotFound(t *testing.T) {
	ctx := context.Background()
	names, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	mock := &testutil.MockCompute{
		Servers: []*provider.Server{},
	}

	_, err = FindMaster(ctx, mock, names)
	if err == nil {
		t.Fatal("FindMaster: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no master server found") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "no master server found")
	}
}
