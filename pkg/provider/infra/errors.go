package infra

import "errors"

// Sentinel errors every IaaS backend wraps with `%w` so callers classify
// via errors.Is — never via strings.Contains on error messages. The
// whole point of this file is that `strings.Contains(err.Error(),
// "locked")` / `"not attached"` / `"already added"` / `"in use"` leaves
// the codebase. Matching error messages is brittle (wire format drifts
// between API versions, localization changes strings), so providers
// inspect status codes / structured error bodies and wrap with the
// right sentinel. Callers never parse strings again.
//
// Not every inline error class is represented here — only the ones we
// actually branch on. utils.ErrNotFound (in pkg/utils/httpclient.go) is
// reused for 404s; no parallel ErrNotFound in this package.
var (
	// ErrLocked — resource is being mutated server-side and the caller
	// should retry the entire operation. Hetzner surfaces this as HTTP
	// 423; Scaleway returns 409 with a "locked" marker. Both providers
	// wrap into ErrLocked so the shared retry loop classifies uniformly.
	ErrLocked = errors.New("resource locked")

	// ErrInUse — resource still has dependents (a firewall attached to a
	// server that hasn't fully terminated, a volume still claimed by a
	// pod). Caller retries until the dependent goes away or times out.
	// Scaleway's "group is in use" during SG delete is the canonical case.
	ErrInUse = errors.New("resource in use")

	// ErrAlreadyAttached — attach operation would be a no-op (resource
	// is already attached to the requested target). Caller treats as
	// idempotent success. Hetzner's "already added" on firewall-apply is
	// the canonical case.
	ErrAlreadyAttached = errors.New("already attached")

	// ErrNotAttached — detach operation would be a no-op (resource is
	// not currently attached to anything). Caller treats as idempotent
	// success. Hetzner's "not attached" on volume-detach is the
	// canonical case.
	ErrNotAttached = errors.New("not attached")
)
