package infra

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// MountVolume mounts a volume on the server at mountPath.
// devicePath is the OS block device — resolved by the provider via ResolveDevicePath.
// Idempotent: skips if already mounted at mountPath.
func MountVolume(ctx context.Context, ssh utils.SSHClient, devicePath string, mountPath string, w io.Writer) error {
	// Already mounted? Grow filesystem in case of resize, then return.
	out, err := ssh.Run(ctx, fmt.Sprintf("mountpoint -q %s && echo mounted || echo not", mountPath))
	if err == nil && strings.TrimSpace(string(out)) == "mounted" {
		_, _ = ssh.Run(ctx, fmt.Sprintf("sudo xfs_growfs %s 2>/dev/null || true", mountPath))
		fmt.Fprintf(w, "already mounted at %s\n", mountPath)
		return nil
	}

	// Wait for device node
	if err := waitForDevice(ctx, ssh, devicePath); err != nil {
		return fmt.Errorf("wait for device %s: %w", devicePath, err)
	}

	// Create mount directory
	if _, err := ssh.Run(ctx, fmt.Sprintf("sudo mkdir -p %s", mountPath)); err != nil {
		return fmt.Errorf("mkdir %s: %w", mountPath, err)
	}

	// Format XFS if needed
	out, err = ssh.Run(ctx, fmt.Sprintf("sudo blkid %s || true", devicePath))
	if err == nil && !strings.Contains(string(out), "TYPE=") {
		fmt.Fprintf(w, "formatting %s as XFS...\n", devicePath)
		if _, err := ssh.Run(ctx, fmt.Sprintf("sudo mkfs.xfs %s", devicePath)); err != nil {
			return fmt.Errorf("format %s: %w", devicePath, err)
		}
	}

	// Mount
	if _, err := ssh.Run(ctx, fmt.Sprintf("sudo mount %s %s", devicePath, mountPath)); err != nil {
		return fmt.Errorf("mount %s → %s: %w", devicePath, mountPath, err)
	}

	// Add fstab entry if not present
	out, err = ssh.Run(ctx, fmt.Sprintf("grep '%s' /etc/fstab || true", mountPath))
	if err == nil && strings.TrimSpace(string(out)) == "" {
		fstabCmd := fmt.Sprintf(
			`UUID=$(sudo blkid -s UUID -o value %s) && echo "UUID=$UUID %s xfs defaults,nofail 0 2" | sudo tee -a /etc/fstab`,
			devicePath, mountPath,
		)
		if _, err := ssh.Run(ctx, fstabCmd); err != nil {
			return fmt.Errorf("fstab entry: %w", err)
		}
	}

	// Grow filesystem to fill device (no-op if already full size)
	_, _ = ssh.Run(ctx, fmt.Sprintf("sudo xfs_growfs %s 2>/dev/null || true", mountPath))

	// Verify
	out, err = ssh.Run(ctx, fmt.Sprintf("mountpoint -q %s && echo mounted || echo not", mountPath))
	if err != nil || strings.TrimSpace(string(out)) != "mounted" {
		return fmt.Errorf("volume not mounted at %s after mount attempt", mountPath)
	}

	fmt.Fprintf(w, "mounted at %s\n", mountPath)
	return nil
}

// UnmountVolume unmounts a volume and removes the fstab entry.
// No-op if not mounted. Non-fatal errors are returned (caller decides severity).
func UnmountVolume(ctx context.Context, ssh utils.SSHClient, mountPath string, w io.Writer) error {
	// Check if mounted
	out, err := ssh.Run(ctx, fmt.Sprintf("mountpoint -q %s && echo mounted || echo not", mountPath))
	if err != nil || strings.TrimSpace(string(out)) != "mounted" {
		return nil // not mounted, nothing to do
	}

	// Unmount
	fmt.Fprintf(w, "unmounting %s...\n", mountPath)
	if _, err := ssh.Run(ctx, fmt.Sprintf("sudo umount -f %s", mountPath)); err != nil {
		return fmt.Errorf("umount %s: %w", mountPath, err)
	}

	// Remove fstab entry
	if _, err := ssh.Run(ctx, fmt.Sprintf("sudo sed -i '\\|%s|d' /etc/fstab", mountPath)); err != nil {
		return fmt.Errorf("fstab cleanup: %w", err)
	}

	// Remove mount directory
	_, _ = ssh.Run(ctx, fmt.Sprintf("sudo rmdir %s 2>/dev/null || true", mountPath))

	fmt.Fprintf(w, "unmounted %s\n", mountPath)
	return nil
}

func waitForDevice(ctx context.Context, ssh utils.SSHClient, devicePath string) error {
	return utils.Poll(ctx, 2*time.Second, time.Minute, func() (bool, error) {
		out, err := ssh.Run(ctx, fmt.Sprintf("test -b %s && echo ready || true", devicePath))
		if err != nil {
			return false, nil
		}
		return strings.TrimSpace(string(out)) == "ready", nil
	})
}
