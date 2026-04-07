# Phase 4: One Production Surface

## Goal

Finish the move to a boring product surface:

- baseline production behavior is expressed declaratively
- deploy config/API is the source of truth
- CLI commands do not introduce separate production semantics

At the end of this phase, there should not be two ways of thinking about deployment, and edge-provider behavior should clearly read as an overlay on top of the baseline model.

This phase must also guarantee that the declarative surface can express cleanup and transition paths without leaving dead zones.

## Why this phase exists

The branch currently has a split personality:

- deploy config supports firewall/domains/proxy concepts
- imperative `ingress apply` carries extra production semantics like provided cert behavior

That means:

- some production behavior lives in config
- some lives only in imperative flags

This is a direct source of clutter.

This phase should convert the boundary rules into the public product surface:

- if a boundary matters in production, it must be visible in config/API
- no implicit subsystem coupling may survive only in imperative wrappers

This phase must also reflect the intended layering:

- baseline deploy concepts should stay generic
- edge/distribution capabilities should be attached explicitly
- Cloudflare-specific options should not define the baseline shape of config

Cloudflare concerns are allowed to live only in:

- explicit overlay-specific config sections/fields, if they exist
- overlay-specific validation branches that activate only when Cloudflare overlay behavior is requested
- overlay-specific planner/executor branches carrying explicitly requested Cloudflare behavior
- Cloudflare-specific docs/examples for that overlay path

Cloudflare concerns must not define:

- baseline config semantics
- generic validation semantics
- generic planner semantics
- generic executor semantics
- generic ingress/firewall/TLS concepts exposed through the public surface

## Boundary summary for this phase

- production semantics belong to declarative config/API
- CLI wrappers may expose convenience, but not separate subsystem ownership rules
- deploy, executor, and CLI must all enforce the same boundaries
- no production boundary may exist only as "tribal knowledge" in command code
- baseline concepts and overlay concepts must be distinguishable in the public surface
- Cloudflare-specific public semantics must stay inside explicit overlay branches only

## Scope

Primary files:

- `internal/api/config/schema.go`
- `internal/api/config/validate.go`
- `internal/api/plan/plan.go`
- `internal/api/handlers/executor.go`
- `internal/core/ingress.go`
- any API/UI contract around deploy config

## Required changes

### 1. Put supported ingress/TLS behavior into config/API

If a feature is production-supported, it must be representable in config.

That means deciding how the deploy model expresses:

- direct ingress
- optional edge-proxied ingress
- provided TLS material, if retained
- any retained edge-helper behavior such as Origin CA

The exact schema can vary, but the rule is fixed:

- no production-only feature should exist solely as a CLI flag path
- edge-provider features should read as add-ons to baseline exposure, not as the baseline itself

### 2. Update validation to enforce the full model

In `internal/api/config/validate.go`:

- validate proxy/direct expectations
- validate firewall compatibility
- validate TLS mode compatibility
- reject impossible or unsupported combinations early

The user should fail at config validation time, not after partial deploy behavior starts.

Validation should also preserve the layering:

- baseline validation remains generic
- provider-specific overlay validation activates only when the overlay is requested

Validation must not let Cloudflare shape the generic model.

Validation should also reject states that would create frozen cleanup gaps.

Examples to cover:

- partial domain removal
- full ingress removal
- service removal while ingress still depends on it
- TLS mode transitions that would otherwise leave ambiguous ownership

### 3. Make planning reflect the same model

In `internal/api/plan/plan.go`:

- emit steps from the declarative model only
- ensure the ingress step receives all data it needs to fully reconcile desired ingress behavior
- avoid reconstructing or inferring unsupported state later

The plan should clearly carry the chosen ingress behavior into execution.
It should also keep baseline vs overlay concerns visible rather than collapsing them into opaque special cases.

Cloudflare-specific planning must remain explicit overlay planning, not hidden generic-plan behavior.

The planner must also express partial and destructive transitions correctly.

Examples:

- removing one domain while keeping another
- removing all domains without deleting the service
- deleting a service only after dependent ingress state has been removed
- removing overlay behavior without destabilizing the baseline resource model

### 4. Make executor and CLI wrappers thin

In:

- `internal/api/handlers/executor.go`
- `internal/core/ingress.go`

Make both paths call the same underlying runtime logic with the same concepts.

The CLI should be:

- a convenience wrapper
- not a second product model

That includes boundary enforcement:

- CLI commands may validate neighboring state
- CLI commands may not introduce hidden cross-subsystem mutations unavailable from deploy

### 5. Remove CLI-only production semantics

After config support exists:

- remove or demote flag combinations that would still create behavior unavailable from deploy config
- ensure command help text reflects the single supported model

## Boundary checks to close in this phase

- every supported production behavior is representable in config/API
- no CLI flag introduces hidden cross-subsystem mutation
- deploy and CLI wrappers enforce the same guard rules
- the public surface teaches one ownership model only
- the public surface makes baseline behavior and optional edge overlays visibly distinct
- the public surface can express cleanup and transition states without relying on hidden runtime tricks
- Cloudflare-specific behavior is isolated to explicit overlay branches in config/validation/planning/execution

## Tests to add or update

Add/update tests proving:

- config can express every supported ingress mode
- invalid combinations fail in validation
- deploy planner carries the same ingress semantics as the CLI path
- executor and CLI wrappers both hit the same runtime behavior
- config/planner handle partial removals and transition states explicitly

## Completion criteria

This phase is complete only when:

- production ingress behavior is declarative
- CLI and deploy do not diverge semantically
- the product has one production surface, not two
- Cloudflare-specific behavior reads as an optional overlay, not as baseline configuration gravity
- Cloudflare-specific behavior does not define any generic public concept
