package infra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/core"
)

// WaitHTTPS polls until https://domain returns a non-5xx response.
// Parallel to WaitSSH — waits for a service to become reachable.
func WaitHTTPS(ctx context.Context, domain string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	return core.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		resp, err := client.Get("https://" + domain)
		if err != nil {
			return false, nil
		}
		resp.Body.Close()
		return resp.StatusCode >= 200 && resp.StatusCode < 500, nil
	})
}

// WaitSSH polls until SSH is reachable on addr (host:port).
func WaitSSH(ctx context.Context, addr string, privateKey []byte) error {
	return core.Poll(ctx, 2*time.Second, 3*time.Minute, func() (bool, error) {
		conn, err := ConnectSSH(ctx, addr, core.DefaultUser, privateKey)
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
func EnsureDocker(ctx context.Context, ip string, privateKey []byte) error {
	ssh, err := ConnectSSH(ctx, ip+":22", core.DefaultUser, privateKey)
	if err != nil {
		return err
	}
	defer ssh.Close()

	// Already installed?
	if _, err := ssh.Run(ctx, "sudo docker info >/dev/null 2>&1"); err == nil {
		return nil
	}

	// Wait for cloud-init to finish
	_, _ = ssh.Run(ctx, "cloud-init status --wait >/dev/null 2>&1 || true")

	install := func() error {
		for _, cmd := range []string{
			"sudo timeout 120 bash -lc 'while fuser /var/lib/dpkg/lock >/dev/null 2>&1 || fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 2; done'",
			"sudo dpkg --configure -a >/dev/null 2>&1 || true",
			"curl -fsSL https://get.docker.com | sudo sh",
			"sudo usermod -aG docker " + core.DefaultUser,
			"sudo systemctl enable docker",
			"sudo systemctl start docker",
		} {
			if _, err := ssh.Run(ctx, cmd); err != nil {
				return fmt.Errorf("docker: %s: %w", strings.Split(cmd, " ")[0], err)
			}
		}
		return nil
	}

	if err := core.Poll(ctx, 5*time.Second, 3*time.Minute, func() (bool, error) {
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

	// Verify with fresh session (picks up docker group)
	ssh.Close()
	return core.Poll(ctx, 2*time.Second, time.Minute, func() (bool, error) {
		fresh, err := ConnectSSH(ctx, ip+":22", core.DefaultUser, privateKey)
		if err != nil {
			return false, nil
		}
		defer fresh.Close()
		_, err = fresh.Run(ctx, "docker info >/dev/null 2>&1")
		return err == nil, nil
	})
}
