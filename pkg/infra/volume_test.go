package infra

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/pkg/testutil"
)

func TestMountVolume_AlreadyMounted(t *testing.T) {
	mountPath := "/mnt/data"

	mountpointCmd := fmt.Sprintf("mountpoint -q %s && echo mounted || echo not", mountPath)
	growfsCmd := fmt.Sprintf("sudo xfs_growfs %s 2>/dev/null || true", mountPath)

	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		mountpointCmd: {Output: []byte("mounted\n")},
		growfsCmd:     {},
	})

	var buf bytes.Buffer
	err := MountVolume(context.Background(), mock, "/dev/sda1", mountPath, &buf)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "already mounted") {
		t.Errorf("expected output to contain 'already mounted', got: %q", buf.String())
	}
}

func TestUnmountVolume_NotMounted(t *testing.T) {
	mountPath := "/mnt/data"

	mountpointCmd := fmt.Sprintf("mountpoint -q %s && echo mounted || echo not", mountPath)

	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		mountpointCmd: {Output: []byte("not\n")},
	})

	var buf bytes.Buffer
	err := UnmountVolume(context.Background(), mock, mountPath, &buf)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	// Should be a no-op — no output expected
	if buf.String() != "" {
		t.Errorf("expected no output for not-mounted volume, got: %q", buf.String())
	}
}

func TestUnmountVolume_Mounted(t *testing.T) {
	mountPath := "/mnt/data"

	mountpointCmd := fmt.Sprintf("mountpoint -q %s && echo mounted || echo not", mountPath)
	umountCmd := fmt.Sprintf("sudo umount -f %s", mountPath)
	sedCmd := fmt.Sprintf("sudo sed -i '\\|%s|d' /etc/fstab", mountPath)
	rmdirCmd := fmt.Sprintf("sudo rmdir %s 2>/dev/null || true", mountPath)

	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		mountpointCmd: {Output: []byte("mounted\n")},
		umountCmd:     {},
		sedCmd:        {},
		rmdirCmd:      {},
	})

	var buf bytes.Buffer
	err := UnmountVolume(context.Background(), mock, mountPath, &buf)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "unmounting") {
		t.Errorf("expected output to contain 'unmounting', got: %q", buf.String())
	}
}
