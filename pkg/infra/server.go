package infra

import (
	"context"
	"errors"
	"fmt"
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

// WaitCloudInit blocks until cloud-init has fully applied userdata on the
// remote — runcmd, packages, write_files, the lot. Canonical Ubuntu gate
// via `cloud-init status --wait` which exits 0 on success, non-zero on
// failure or timeout.
//
// Why every post-SSH step needs this, not just the builder path: SSH
// becomes reachable very early in Ubuntu's boot (ssh.service starts before
// cloud-init runcmd completes). Any step that assumes a package installed
// in cloud-init (apt install docker-ce, the nvoi installer line, etc.) is
// racing against a partial boot otherwise. Previously the first post-SSH
// command was `systemctl enable --now docker.service` which would silently
// fail on fast networks where SSH beat apt; the failure only surfaced when
// a dispatch later hit `command not found`. This helper turns that into a
// loud, at-provision-time failure.
//
// Remote call is idempotent — if cloud-init is already done, returns
// immediately. Runs under sudo because the status file lives in
// /var/lib/cloud.
func WaitCloudInit(ctx context.Context, ssh utils.SSHClient) error {
	if _, err := ssh.Run(ctx, "sudo cloud-init status --wait"); err != nil {
		return fmt.Errorf("cloud-init did not reach done state: %w", err)
	}
	return nil
}
