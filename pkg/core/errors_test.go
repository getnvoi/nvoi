package core

import (
	"errors"
	"testing"
)

func TestNotFoundError(t *testing.T) {
	err := ErrNotFound("volume", "pgdata")
	if err.Error() != `volume "pgdata" not found` {
		t.Fatalf("got %q", err.Error())
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatal("should be NotFoundError")
	}
	if nf.Resource != "volume" || nf.Name != "pgdata" {
		t.Fatalf("fields: %+v", nf)
	}
}

func TestNotFoundError_NoName(t *testing.T) {
	err := ErrNotFound("credentials", "")
	if err.Error() != "credentials not found" {
		t.Fatalf("got %q", err.Error())
	}
}

func TestInputError(t *testing.T) {
	err := ErrInput("--image is required")
	if err.Error() != "--image is required" {
		t.Fatalf("got %q", err.Error())
	}
	var ie *InputError
	if !errors.As(err, &ie) {
		t.Fatal("should be InputError")
	}
}

func TestInputErrorf(t *testing.T) {
	err := ErrInputf("invalid mount %q", "bad:mount:extra")
	if err.Error() != `invalid mount "bad:mount:extra"` {
		t.Fatalf("got %q", err.Error())
	}
	var ie *InputError
	if !errors.As(err, &ie) {
		t.Fatal("should be InputError")
	}
}

func TestNotReadyError(t *testing.T) {
	err := ErrNotReady("database not deployed")
	if err.Error() != "database not deployed" {
		t.Fatalf("got %q", err.Error())
	}
	var nr *NotReadyError
	if !errors.As(err, &nr) {
		t.Fatal("should be NotReadyError")
	}
}

func TestErrorTypes_NotConfused(t *testing.T) {
	nf := ErrNotFound("x", "y")
	ie := ErrInput("z")
	nr := ErrNotReady("w")

	// NotFound is not Input
	var input *InputError
	if errors.As(nf, &input) {
		t.Fatal("NotFoundError should not match InputError")
	}

	// Input is not NotFound
	var notFound *NotFoundError
	if errors.As(ie, &notFound) {
		t.Fatal("InputError should not match NotFoundError")
	}

	// NotReady is neither
	if errors.As(nr, &notFound) {
		t.Fatal("NotReadyError should not match NotFoundError")
	}
	if errors.As(nr, &input) {
		t.Fatal("NotReadyError should not match InputError")
	}
}
