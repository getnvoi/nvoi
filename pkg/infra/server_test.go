package infra

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
)

func TestEnsureDocker_AlreadyInstalled(t *testing.T) {
	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		"sudo docker info >/dev/null 2>&1": {},
	})

	err := ensureDocker(context.Background(), mock)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}
