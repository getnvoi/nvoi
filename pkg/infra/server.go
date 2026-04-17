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
