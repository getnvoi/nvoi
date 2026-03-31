package core

import (
	"context"
	"errors"
	"time"
)

var ErrTimeout = errors.New("poll: timeout exceeded")

// Poll calls fn every interval until it returns true or the timeout elapses.
func Poll(ctx context.Context, interval, timeout time.Duration, fn func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return ErrTimeout
		}
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		done, err := fn()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(interval):
		}
	}
}
