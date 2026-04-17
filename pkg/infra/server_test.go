package infra

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
)

var errTest = errors.New("test error")

func TestEnsureDocker_AlreadyInstalled(t *testing.T) {
	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		"sudo docker info >/dev/null 2>&1": {},
	})

	err := EnsureDocker(context.Background(), mock, io.Discard)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

// errTest kept for parity with the rest of the package's test helpers.
var _ = errTest
