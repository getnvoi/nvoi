// Package ensure holds the minimal orchestration skeletons shared by
// the IaaS backends. Small on purpose — anything that would require
// leaking provider-specific types through the seam stays in the
// provider file. The payoff isn't LOC reduction; it's making the
// load-bearing sequencing (find → detach dependents → delete → wait)
// impossible to get wrong in a new backend.
package ensure

import (
	"context"
	"errors"
	"time"

	"github.com/getnvoi/nvoi/pkg/provider/infra"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// DeletePlan describes a delete operation end-to-end. Providers fill in
// the callbacks; the skeleton enforces ordering:
//
//  1. Find — locate the resource by name. Returns (id, true) when found,
//     ("", false) when already gone.
//  2. PreDelete — run each step in declared order BEFORE the final
//     DELETE call. For servers this is [detach-firewalls,
//     detach-volumes] — CLAUDE.md Key Rule #11. Skipping a step is not
//     possible through this interface.
//  3. Delete — the final DELETE API call. A utils.ErrNotFound return
//     is swallowed (idempotent — something else raced us to it).
//  4. WaitGone — optional post-delete poll. Uses infra.PollInterval
//     with the supplied Timeout (infra.PollFast / PollSlow).
//
// PreDelete steps receive the resource ID so each callback closes over
// only the provider client, not the name resolution. Errors bubble up
// verbatim — providers are responsible for wrapping with %w to the
// infra sentinels so higher-level retry machinery classifies correctly.
type DeletePlan struct {
	// Find locates the resource by name. (id, true, nil) on hit;
	// ("", false, nil) on idempotent miss; ("", false, err) on transport
	// error.
	Find func(ctx context.Context) (id string, found bool, err error)

	// PreDelete is the ordered list of steps to run before the final
	// DELETE call. Nil or empty is valid (firewalls, volumes have no
	// dependents to detach). Each step closes over the provider client.
	PreDelete []func(ctx context.Context, id string) error

	// Delete is the final delete call. utils.ErrNotFound is swallowed;
	// every other error bubbles up.
	Delete func(ctx context.Context, id string) error

	// WaitGone, when non-nil, polls until the resource is confirmed
	// gone. Uses infra.PollInterval with Timeout as the budget.
	// Transient API errors during the poll must return (false, nil) —
	// retry — NOT (false, err). Returning an error aborts the poll.
	WaitGone func(ctx context.Context) (bool, error)

	// Timeout is the budget for WaitGone. Ignored when WaitGone is nil.
	// Callers pick infra.PollFast for attach-style ops, infra.PollSlow
	// for server-termination-style ops.
	Timeout time.Duration
}

// Delete runs the plan. Returns nil when the resource is confirmed gone
// (or already wasn't there); returns a wrapped error otherwise.
func Delete(ctx context.Context, plan DeletePlan) error {
	id, found, err := plan.Find(ctx)
	if err != nil {
		return err
	}
	if !found {
		return nil // idempotent — already gone
	}
	for _, step := range plan.PreDelete {
		if err := step(ctx, id); err != nil {
			return err
		}
	}
	if err := plan.Delete(ctx, id); err != nil && !errors.Is(err, utils.ErrNotFound) {
		return err
	}
	if plan.WaitGone == nil {
		return nil
	}
	return utils.Poll(ctx, infra.PollInterval, plan.Timeout, func() (bool, error) {
		return plan.WaitGone(ctx)
	})
}
