package handlers

import (
	"fmt"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

func TestHumaError_NotFound(t *testing.T) {
	err := humaError(pkgcore.ErrNotFound("volume", "pgdata"))
	se, ok := err.(huma.StatusError)
	if !ok {
		t.Fatal("expected huma.StatusError")
	}
	if se.GetStatus() != 404 {
		t.Fatalf("status = %d, want 404", se.GetStatus())
	}
}

func TestHumaError_Input(t *testing.T) {
	err := humaError(pkgcore.ErrInput("bad input"))
	se, ok := err.(huma.StatusError)
	if !ok {
		t.Fatal("expected huma.StatusError")
	}
	if se.GetStatus() != 400 {
		t.Fatalf("status = %d, want 400", se.GetStatus())
	}
}

func TestHumaError_NotReady(t *testing.T) {
	err := humaError(pkgcore.ErrNotReady("not deployed"))
	se, ok := err.(huma.StatusError)
	if !ok {
		t.Fatal("expected huma.StatusError")
	}
	if se.GetStatus() != 422 {
		t.Fatalf("status = %d, want 422", se.GetStatus())
	}
}

func TestHumaError_Generic(t *testing.T) {
	err := humaError(fmt.Errorf("ssh connection failed"))
	se, ok := err.(huma.StatusError)
	if !ok {
		t.Fatal("expected huma.StatusError")
	}
	if se.GetStatus() != 500 {
		t.Fatalf("status = %d, want 500", se.GetStatus())
	}
}
