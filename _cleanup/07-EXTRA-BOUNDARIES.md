# Phase 7: Residual Global Boundaries

## Goal

Apply the same strict rule used in phase 1 everywhere else:

- no cross-subsystem mutation
- read-only inspection allowed for guards
- unsafe operations fail hard

This phase exists only for boundaries that do not belong cleanly to phases 1-6, or that require a final whole-system enforcement pass after those phases are complete.

It also exists to verify the final layering is respected:

- baseline behavior remains baseline
- optional edge-provider behavior remains an overlay
- no late-stage coupling pulls Cloudflare-specific behavior back into the core mental model

## Residual boundaries to enforce

### 1. `service delete` must not silently remove ingress or DNS

- if ingress still targets the service, deletion should fail hard
- ingress cleanup must happen first
- DNS cleanup may happen only after ingress no longer references the domains

Areas to check:

- service deletion flow in `pkg/core/service.go`
- deploy ordering in `internal/api/plan/plan.go`
- executor behavior in `internal/api/handlers/executor.go`

This boundary is global because it spans service lifecycle, ingress ownership, and DNS sequencing.

### 2. ingress reconciliation must not silently delete DNS

- ingress owns routes only
- if route removal leaves DNS orphaned, that is acceptable temporarily
- DNS cleanup remains explicit or deploy-driven

Areas to check:

- ingress reconciliation in `pkg/core/dns.go`
- deploy diff behavior in `internal/api/plan/plan.go`

This boundary is global because it is the mirror check to the guarded DNS delete rule from phase 1.

### 3. Guard behavior must be consistent everywhere

- neighboring subsystems may be inspected for safety
- neighboring subsystems may not be mutated implicitly
- destructive operations that would violate dependencies must fail hard
- every guard failure must instruct the user toward the owning subsystem

Areas to check:

- command wrappers in `internal/core/`
- planner/executor in `internal/api/`
- runtime operations in `pkg/core/`

### 4. Overlay behavior must stay visibly optional

- baseline deploy behavior must remain fully understandable without Cloudflare
- Cloudflare-specific capabilities must activate only when explicitly requested
- future edge capabilities such as tunnels or Access should be addable without reshaping the baseline model

Areas to check:

- deploy-facing config and validation in `internal/api/config/`
- runtime mode resolution in `pkg/core/`
- docs/examples for whether Cloudflare feels like a top layer or a baseline assumption

### 5. Every guard must leave a valid cleanup path

- no command should be guarded in a way that freezes legitimate partial cleanup forever
- if one subsystem blocks an operation, another subsystem must clearly own the allowed next step
- partial removals, full removals, and transitions must all remain achievable

Areas to check:

- planner ordering in `internal/api/plan/`
- destructive runtime paths in `pkg/core/`
- command help/docs for whether the allowed cleanup path is obvious

## Completion criteria

- every destructive command respects ownership boundaries
- every guard follows the same pattern: inspect, reject, instruct
- no subsystem still performs "helpful" mutation of adjacent state
- baseline vs overlay layering still reads cleanly at the end
- no guard creates a dead cleanup area or frozen transition path
