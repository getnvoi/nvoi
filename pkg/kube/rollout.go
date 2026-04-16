package kube

import (
	"time"
)

// ProgressEmitter receives status updates during rollout/job polling.
// Defined here so kube/ doesn't import app/. app.Output satisfies this.
type ProgressEmitter interface {
	Progress(msg string)
}

// rolloutPollInterval is the interval between readiness polls.
var rolloutPollInterval = 3 * time.Second

// rolloutTimeout is the maximum time to wait for all pods to become ready.
var rolloutTimeout = 5 * time.Minute

// stabilityDelay is the pause between "all ready" and the verification poll.
var stabilityDelay = 4 * time.Second

// SetTestTiming overrides poll interval, stability delay, and timeouts for tests.
func SetTestTiming(poll, stability time.Duration) {
	rolloutPollInterval = poll
	stabilityDelay = stability
	rolloutTimeout = 50 * time.Millisecond
	jobPollInterval = poll
	jobTimeout = 50 * time.Millisecond
}
