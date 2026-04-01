package infra

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/provider"
)

// MountVolume resolves the device path and mounts a volume on the server.
// The volume must already be attached (via provider API).
// Idempotent: skips if already mounted at mountPath.
func MountVolume(ctx context.Context, vol *provider.Volume, serverIP string, mountPath string, privKey []byte) error {
	ssh, err := ConnectSSH(ctx, serverIP+":22", core.DefaultUser, privKey)
	if err != nil {
		return fmt.Errorf("ssh for volume mount: %w", err)
	}
	defer ssh.Close()

	// Already mounted? Grow filesystem in case of resize, then return.
	out, err := ssh.Run(ctx, fmt.Sprintf("mountpoint -q %s && echo mounted || echo not", mountPath))
	if err == nil && strings.TrimSpace(string(out)) == "mounted" {
		_, _ = ssh.Run(ctx, fmt.Sprintf("sudo xfs_growfs %s 2>/dev/null || true", mountPath))
		fmt.Printf("  already mounted at %s\n", mountPath)
		return nil
	}

	// Resolve device path — poll until available
	devicePath, err := resolveDevicePath(ctx, vol, ssh)
	if err != nil {
		return fmt.Errorf("resolve device path for %s: %w", vol.Name, err)
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
		fmt.Printf("  formatting %s as XFS...\n", devicePath)
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

	fmt.Printf("  ✓ mounted at %s\n", mountPath)
	return nil
}

// UnmountVolume unmounts a volume and removes the fstab entry.
// No-op if not mounted. Non-fatal errors are returned (caller decides severity).
func UnmountVolume(ctx context.Context, serverIP string, mountPath string, privKey []byte) error {
	ssh, err := ConnectSSH(ctx, serverIP+":22", core.DefaultUser, privKey)
	if err != nil {
		return fmt.Errorf("ssh: %w", err)
	}
	defer ssh.Close()

	// Check if mounted
	out, err := ssh.Run(ctx, fmt.Sprintf("mountpoint -q %s && echo mounted || echo not", mountPath))
	if err != nil || strings.TrimSpace(string(out)) != "mounted" {
		return nil // not mounted, nothing to do
	}

	// Unmount
	fmt.Printf("  unmounting %s...\n", mountPath)
	if _, err := ssh.Run(ctx, fmt.Sprintf("sudo umount -f %s", mountPath)); err != nil {
		return fmt.Errorf("umount %s: %w", mountPath, err)
	}

	// Remove fstab entry
	if _, err := ssh.Run(ctx, fmt.Sprintf("sudo sed -i '\\|%s|d' /etc/fstab", mountPath)); err != nil {
		return fmt.Errorf("fstab cleanup: %w", err)
	}

	// Remove mount directory
	_, _ = ssh.Run(ctx, fmt.Sprintf("sudo rmdir %s 2>/dev/null || true", mountPath))

	fmt.Printf("  ✓ unmounted %s\n", mountPath)
	return nil
}

func resolveDevicePath(ctx context.Context, vol *provider.Volume, ssh core.SSHClient) (string, error) {
	// If provider already gave us a device path, use it
	if vol.DevicePath != "" {
		return vol.DevicePath, nil
	}

	// Poll: check /dev/disk/by-id/ for the volume ID
	var devicePath string
	err := core.Poll(ctx, 2*time.Second, time.Minute, func() (bool, error) {
		out, err := ssh.Run(ctx, fmt.Sprintf("ls /dev/disk/by-id/ 2>/dev/null | grep -i '%s' || true", vol.ID))
		if err == nil {
			line := strings.TrimSpace(string(out))
			if line != "" {
				lines := strings.Split(line, "\n")
				devicePath = "/dev/disk/by-id/" + strings.TrimSpace(lines[0])
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("device path not available for volume %s: %w", vol.ID, err)
	}
	return devicePath, nil
}

func waitForDevice(ctx context.Context, ssh core.SSHClient, devicePath string) error {
	return core.Poll(ctx, 2*time.Second, time.Minute, func() (bool, error) {
		out, err := ssh.Run(ctx, fmt.Sprintf("test -b %s && echo ready || true", devicePath))
		if err != nil {
			return false, nil
		}
		return strings.TrimSpace(string(out)) == "ready", nil
	})
}
