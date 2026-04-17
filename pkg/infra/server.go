package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
)

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
// Writes install progress to w (same pattern as InstallK3sMaster).
func EnsureDocker(ctx context.Context, ssh utils.SSHClient, w io.Writer) error {
	// Already installed? Still ensure user is in docker group (some images
	// ship Docker pre-installed but the deploy user isn't in the group).
	if _, err := ssh.Run(ctx, "sudo docker info >/dev/null 2>&1"); err == nil {
		_, _ = ssh.Run(ctx, "sudo usermod -aG docker "+utils.DefaultUser)
		fmt.Fprintln(w, "docker already installed")
		return nil
	}

	// Wait for cloud-init to finish — networking and package state depend on it.
	// We only care that it completed, not whether it succeeded.
	// cloud-init may not exist on some images — that's fine.
	fmt.Fprintln(w, "waiting for cloud-init...")
	ssh.Run(ctx, "cloud-init status --wait >/dev/null 2>&1")

	install := func() error {
		type step struct {
			name   string
			cmd    string
			stream bool
		}
		steps := []step{
			{"waiting for dpkg lock", "sudo timeout 120 bash -lc 'while fuser /var/lib/dpkg/lock >/dev/null 2>&1 || fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 2; done'", false},
			{"reconfiguring dpkg", "sudo dpkg --configure -a >/dev/null 2>&1 || true", false},
			{"installing docker", "curl -fsSL https://get.docker.com | sudo sh", true},
			{"adding deploy user to docker group", "sudo usermod -aG docker " + utils.DefaultUser, false},
			{"enabling docker service", "sudo systemctl enable docker", false},
			{"starting docker service", "sudo systemctl start docker", false},
		}
		for _, s := range steps {
			fmt.Fprintln(w, s.name+"...")
			var err error
			if s.stream {
				err = ssh.RunStream(ctx, s.cmd, w, w)
			} else {
				_, err = ssh.Run(ctx, s.cmd)
			}
			if err != nil {
				return fmt.Errorf("docker: %s: %w", s.name, err)
			}
		}
		return nil
	}

	// 6 minutes covers slow arm64 package mirrors + apt unattended-upgrade
	// contention right after cloud-init. Shorter timeouts flake on cax11-class
	// shared-CPU instances.
	if err := utils.Poll(ctx, 5*time.Second, 6*time.Minute, func() (bool, error) {
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
