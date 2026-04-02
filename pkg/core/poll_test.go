package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollImmediateSuccess(t *testing.T) {
	err := Poll(context.Background(), time.Millisecond, 50*time.Millisecond, func() (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestPollSuccessAfterN(t *testing.T) {
	var calls int32
	err := Poll(context.Background(), time.Millisecond, 50*time.Millisecond, func() (bool, error) {
		n := atomic.AddInt32(&calls, 1)
		return n >= 3, nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if c := atomic.LoadInt32(&calls); c < 3 {
		t.Fatalf("expected at least 3 calls, got %d", c)
	}
}

func TestPollTimeout(t *testing.T) {
	err := Poll(context.Background(), 5*time.Millisecond, 50*time.Millisecond, func() (bool, error) {
		return false, nil
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestPollContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Poll(ctx, time.Millisecond, time.Second, func() (bool, error) {
		return false, nil
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestPollErrorPropagation(t *testing.T) {
	sentinel := errors.New("provider unreachable")
	err := Poll(context.Background(), time.Millisecond, 50*time.Millisecond, func() (bool, error) {
		return false, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}
