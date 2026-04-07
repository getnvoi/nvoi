# Phase 3: Finish TLS Ownership

## Goal

Remove the half-owned edge TLS behavior from the branch.

At the end of this phase there must be no "magic" TLS path that exists without lifecycle ownership.

Decision for this phase:

- keep Cloudflare Origin CA auto-generation only as an explicit overlay capability
- fully own its lifecycle in this phase
- do not promote it into the baseline deployment model

Cloudflare concerns are allowed to live only in:

- `pkg/provider/cloudflare/`
- explicit Cloudflare overlay-resolution/helpers in `pkg/core/`
- Cloudflare-specific validation branches that activate only when the Cloudflare overlay is requested
- Cloudflare-specific docs/examples for that overlay path

Cloudflare concerns must not define:

- baseline ingress concepts
- baseline firewall concepts
- baseline TLS ownership
- generic config semantics
- generic planner semantics
- generic Caddy rendering semantics

## Why this phase exists

Right now the branch introduced a powerful feature in an unsafe shape:

- it can auto-generate Cloudflare Origin CA certs
- but it does not clearly persist, reuse, rotate, or revoke them

That means the branch gained complexity without completing the operational model.

This is not acceptable for a deployment tool aiming to be robust.

This phase should also enforce a stronger TLS ownership boundary:

- TLS material may be consumed by ingress
- TLS material generation/reuse must have a single owner
- ingress must not rely on half-owned provider-side magic

This phase must also clarify product layering:

- Origin CA is not a baseline ingress concept
- it is an edge-provider helper capability
- if Cloudflare provides it, it must sit atop the baseline model cleanly

## Boundary summary for this phase

- ingress may use TLS material, but does not get to "half-own" it
- provider integrations may help implement TLS ownership, but may not become an implicit second owner
- CLI/runtime/config must not disagree about where TLS state lives
- unsupported ownership models must be removed, not tolerated
- edge-provider TLS helpers must remain overlays, not baseline assumptions

## Scope

Primary files:

- `pkg/core/dns.go`
- `pkg/provider/cloudflare/origin_ca.go`
- any TLS secret handling in `pkg/kube/caddy.go`
- tests around proxied ingress and origin cert creation

## Required direction

Cloudflare Origin CA automation stays.

It stays only under these conditions:

- it is treated as a Cloudflare overlay capability
- it is not treated as a baseline ingress/TLS concept
- its lifecycle is fully owned now
- its Cloudflare-specific concerns stay inside the allowed overlay areas listed above

Required behavior:

- generated cert/key is owned by the deploy system
- repeated deploys reuse existing material when still valid/applicable
- domain set changes trigger explicit replacement behavior
- old material has a cleanup policy
- storage location and naming are deterministic
- failure behavior is explicit when lifecycle operations cannot be completed

The current "generate on deploy and stuff into a secret" is not enough.
It also preserves the baseline model by refusing to embed a half-finished Cloudflare helper into it.

## Required changes

### 1. Define ownership of generated material

Decide what is authoritative:

- Kubernetes TLS secret
- provider-side metadata
- local/deploy state

Then make the code consistently treat that as the source of truth.

### 2. Reuse instead of reissuing

In `pkg/core/dns.go`:

- before generating a new cert, check whether a valid existing cert/key already exists for the required domains
- do not create a fresh long-lived cert on every deploy

### 3. Handle domain changes deliberately

If proxied domains change:

- decide whether the existing cert is still valid
- if not, replace it explicitly
- define what happens to the old cert

Do not allow silent accumulation of long-lived orphaned certs.

### 4. Define cleanup/revocation story

If the provider API supports cleanup/revocation:

- implement it, or
- document why lifecycle intentionally leaves old certs behind and make that an explicit accepted tradeoff

But do not leave it implicit.

## Custom cert ownership

Regardless of Origin CA decision:
- provided certs are a supported ingress TLS mode only if they have clear product ownership
- they must not remain a side-path bolted onto the CLI only
- TLS ownership must be explicit enough that adjacent subsystems do not need to guess where cert state lives
- Cloudflare-specific TLS behavior must remain clearly optional and layered above the baseline ingress model

This phase must also close TLS dead areas created by transitions.

Examples:

- proxied -> direct should not leave stale edge TLS material with ambiguous meaning
- provided cert -> ACME should not leave old ownership assumptions behind
- ingress removed should not leave TLS state in a half-owned operational limbo unless retention is explicit and intentional

That becomes phase 4 work, but phase 3 must leave the runtime behavior clean and deliberate.

## Boundary checks to close in this phase

- generated TLS material has one authoritative owner
- repeated deploys do not depend on ambiguous cross-layer cert ownership
- no provider-side magic remains without explicit lifecycle control
- no TLS mode remains supported if its ownership is unclear
- Cloudflare-specific TLS capability does not redefine the baseline model
- TLS transitions and TLS removal do not leave ambiguous leftover state

## Tests to add or update

- repeated deploy does not reissue unnecessarily
- domain change behavior is explicit and tested
- stored material is reused predictably
- cleanup or retention behavior after mode/domain removal is explicit and tested

## Completion criteria

This phase is complete only when:

- there is no unmanaged TLS automation left
- every supported TLS path has clear ownership
- the code no longer carries a "smart" feature that the system does not really own
- TLS behavior is explainable without hand-waving across ingress/provider/CLI boundaries
- edge-provider TLS helpers feel attached on top of the baseline rather than interwoven through it
