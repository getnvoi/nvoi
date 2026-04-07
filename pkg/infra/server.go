package infra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// WaitHTTPS polls until https://domain returns a non-5xx response.
// Fails fast on connection refused/timeout (firewall). Waits longer for TLS errors (cert provisioning).
func WaitHTTPS(ctx context.Context, domain string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	connFailures := 0 // consecutive TCP-level failures

	return utils.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		resp, err := client.Get("https://" + domain)
		if err != nil {
			errMsg := err.Error()
			// TCP connection refused or timeout = firewall/port closed.
			// Fail fast after 3 consecutive connection failures (9 seconds).
			if strings.Contains(errMsg, "connection refused") ||
				strings.Contains(errMsg, "i/o timeout") ||
				strings.Contains(errMsg, "no route to host") {
				connFailures++
				if connFailures >= 3 {
					return false, fmt.Errorf("port not reachable (connection refused/timeout) — firewall may be blocking 80/443")
				}
				return false, nil
			}
			// TLS errors = cert not ready yet, keep polling
			connFailures = 0
			return false, nil
		}
		resp.Body.Close()
		connFailures = 0
		return resp.StatusCode >= 200 && resp.StatusCode < 500, nil
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
func EnsureDocker(ctx context.Context, ip string, privateKey []byte) error {
	ssh, err := ConnectSSH(ctx, ip+":22", utils.DefaultUser, privateKey)
	if err != nil {
		return err
	}
	defer ssh.Close()

	if err := ensureDocker(ctx, ssh); err != nil {
		return err
	}

	// Verify with fresh session (picks up docker group)
	ssh.Close()
	return utils.Poll(ctx, 2*time.Second, time.Minute, func() (bool, error) {
		fresh, err := ConnectSSH(ctx, ip+":22", utils.DefaultUser, privateKey)
		if err != nil {
			return false, nil
		}
		defer fresh.Close()
		_, err = fresh.Run(ctx, "docker info >/dev/null 2>&1")
		return err == nil, nil
	})
}

// ensureDocker contains the Docker install logic, testable with a mock SSH client.
func ensureDocker(ctx context.Context, ssh utils.SSHClient) error {
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
