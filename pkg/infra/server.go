package infra

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// WaitForCertificate polls until the ACME certificate file exists on disk.
// certPath is the full path to the Caddy cert JSON file (from Names.CaddyCertPath).
func WaitForCertificate(ctx context.Context, ssh utils.SSHClient, certPath string) error {
	cmd := fmt.Sprintf("sudo test -f %s && echo ready || echo waiting", certPath)
	return utils.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		out, err := ssh.Run(ctx, cmd)
		if err != nil {
			return false, nil
		}
		return strings.TrimSpace(string(out)) == "ready", nil
	})
}

// WaitForHTTPS verifies a domain responds over HTTPS from the server.
// Runs curl via SSH — no dependency on client DNS propagation.
func WaitForHTTPS(ctx context.Context, ssh utils.SSHClient, domain string) error {
	cmd := fmt.Sprintf("curl -fsk --connect-timeout 5 https://%s -o /dev/null -w '%%{http_code}'", domain)
	return utils.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		out, err := ssh.Run(ctx, cmd)
		if err != nil {
			return false, nil
		}
		code := strings.TrimSpace(strings.Trim(string(out), "'"))
		return code >= "200" && code < "500", nil
	})
}

// WaitSSH polls until SSH is reachable on addr (host:port).
func WaitSSH(ctx context.Context, addr string, privateKey []byte) error {
	return utils.Poll(ctx, 2*time.Second, 3*time.Minute, func() (bool, error) {
		conn, err := ConnectSSH(ctx, addr, utils.DefaultUser, privateKey)
		if err != nil {
			if errors.Is(err, ErrHostKeyChanged) {
				return false, err
			}
			if errors.Is(err, ErrAuthFailed) {
				return false, fmt.Errorf("%w for %s — server does not accept this key", ErrAuthFailed, addr)
			}
			return false, nil // transient — retry
		}
		defer conn.Close()
		_, err = conn.Run(ctx, "true")
		return err == nil, nil
	})
}

// EnsureDocker installs Docker if not present and waits until it's ready.
func EnsureDocker(ctx context.Context, ssh utils.SSHClient) error {
	// Already installed? Still ensure user is in docker group (some images
	// ship Docker pre-installed but the deploy user isn't in the group).
	if _, err := ssh.Run(ctx, "sudo docker info >/dev/null 2>&1"); err == nil {
		_, _ = ssh.Run(ctx, "sudo usermod -aG docker "+utils.DefaultUser)
		return nil
	}

	// Wait for cloud-init to finish
	_, _ = ssh.Run(ctx, "cloud-init status --wait >/dev/null 2>&1 || true")

	install := func() error {
		for _, cmd := range []string{
			"sudo timeout 120 bash -lc 'while fuser /var/lib/dpkg/lock >/dev/null 2>&1 || fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 2; done'",
			"sudo dpkg --configure -a >/dev/null 2>&1 || true",
			"curl -fsSL https://get.docker.com | sudo sh",
			"sudo usermod -aG docker " + utils.DefaultUser,
			"sudo systemctl enable docker",
			"sudo systemctl start docker",
		} {
			if _, err := ssh.Run(ctx, cmd); err != nil {
				return fmt.Errorf("docker: %s: %w", strings.Split(cmd, " ")[0], err)
			}
		}
		return nil
	}

	if err := utils.Poll(ctx, 5*time.Second, 3*time.Minute, func() (bool, error) {
		if _, err := ssh.Run(ctx, "sudo docker info >/dev/null 2>&1"); err == nil {
			return true, nil
		}
		if err := install(); err != nil {
			return false, nil
		}
		_, err := ssh.Run(ctx, "sudo docker info >/dev/null 2>&1")
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("docker install did not converge: %w", err)
	}

	return nil
}
