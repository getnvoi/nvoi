# Phase 1: Enforce Boundaries

## Goal

Reach the first boring milestone:

- DNS code only manages DNS records.
- ingress code only manages Caddy routes and TLS.
- firewall code only manages exposure.
- no command mutates a neighboring subsystem as a side effect.

This phase is first because the rest of the cleanup is impossible if ownership is still blurred.

This phase should also establish the rule that will be reused everywhere else:

- neighboring subsystems may be inspected for safety
- neighboring subsystems may not be mutated implicitly
- unsafe operations fail hard with a clear next step

## Why this phase exists

The branch moved toward explicit subsystems, but it did not finish the separation.

Current problem:

- `dns delete` still edits Caddy state and may delete the Caddy deployment.
- this creates hidden behavior across subsystem boundaries.
- it becomes hard to answer simple questions like:
  - "what changes ingress?"
  - "what deletes routes?"
  - "what owns public exposure?"

The correct boundary is stricter than simple separation:

- DNS must not mutate ingress
- DNS must also not be allowed to break ingress silently

So the target rule for this phase is:

- `dns delete` is DNS-only
- `dns delete` is guarded
- if ingress still references the domain, DNS deletion must fail hard
- the user must remove ingress usage first through ingress/deploy

That is exactly the kind of middle ground that makes a deployment tool feel unsafe.

## Scope

Primary files:

- `pkg/core/dns.go`
- `internal/api/plan/plan.go`
- any tests covering DNS delete / ingress cleanup behavior
- any docs/comments that still describe DNS as ingress-affecting

## Required changes

### 1. Remove ingress mutation from DNS delete

In `pkg/core/dns.go`:

- remove the block in `DNSDelete` that:
  - opens SSH
  - reads Caddy routes
  - removes a route from Caddy
  - deletes the Caddy deployment/configmap when routes become empty
- keep `DNSDelete` strictly limited to deleting DNS records

Expected end state:

- `DNSDelete` resolves the DNS provider
- validates deletion is allowed
- deletes the requested records
- returns

Nothing else.

### 2. Add a pre-delete guard for ingress references

`DNSDelete` should still protect the system from accidental breakage.

Required behavior:

- inspect current ingress/Caddy route usage before deleting DNS
- if the requested service/domains are still referenced by ingress, fail with a hard error
- the error must clearly tell the user to remove or reconcile ingress first

This is the key rule:

- DNS does not perform ingress cleanup
- DNS refuses deletion while ingress still depends on the domain

This preserves strong subsystem boundaries without allowing silent breakage.

Implementation areas:

- `pkg/core/dns.go`
- `pkg/kube/caddy.go` route inspection helpers, if needed
- any SSH/read-only ingress inspection path required to check current Caddy state

Important distinction:

- read-only inspection is acceptable here
- mutation is not

### 3. Ensure ingress state is reconciled only by ingress logic

In deploy flow:

- route creation/update/removal must be handled by `IngressApply` or a future explicit ingress delete/reconcile path
- DNS changes must not be relied on to "implicitly" clean ingress

This means:

- review `internal/api/plan/plan.go`
- confirm the desired route set is derived from config `domains`
- confirm ingress reconciliation is the only place that translates desired routes into Caddy state

If route deletion is currently incomplete after removing the DNS side effect, that gap must be fixed in ingress/deploy logic, not reintroduced into DNS.

### 4. Fix deploy ordering to satisfy the guard

This boundary has a direct implication for declarative deploy/destroy flows.

If `dns delete` is guarded, then deploy ordering must not attempt to delete DNS before ingress no longer references the domain.

Review `internal/api/plan/plan.go` and adjust ordering as needed so that:

- ingress reconciliation removes route usage first
- DNS deletion happens only after those domains are no longer referenced by ingress

Do not weaken the guard to preserve old ordering.
The ordering must adapt to the boundary, not the reverse.

This same pattern should become the template for later boundaries:

- DNS guarded by ingress references
- services guarded by ingress references
- firewall guarded by active ingress mode

### 5. Make deploy plan ownership explicit

In `internal/api/plan/plan.go`:

- verify DNS diff creates DNS-only steps
- verify ingress steps are responsible for ingress state only
- if needed, add or adjust ingress reconciliation behavior so route removals are handled there

Desired mental model:

- DNS phase: public names
- ingress phase: Caddy configuration

No bleed-through.

### 6. Clean comments and command descriptions

Search for comments/help text that still describe DNS as also managing ingress.

Examples to correct:

- comments in `pkg/core/dns.go`
- CLI descriptions in `internal/core/dns.go`
- any tests or docs encoding the old coupling as expected behavior

The wording matters here because the branch already says "DNS and ingress are separated"; phase 1 must make that true in code.

## Tests to add or update

Add/update tests proving:

- `DNSDelete` never mutates ingress
- `DNSDelete` fails if the domain is still referenced by ingress
- `DNSDelete` succeeds once ingress usage has been removed
- route deletion is handled through ingress/deploy reconciliation, not DNS
- declarative deploy/destroy ordering removes ingress usage before DNS deletion

If an existing test assumes DNS delete also cleans ingress, rewrite it to reflect the new rule:

- ingress first
- DNS second

## Completion criteria

This phase is complete only when all of the following are true:

- deleting DNS records never edits Caddy
- deleting DNS records is blocked while ingress still references those domains
- deleting routes never happens from DNS code
- deploy plan ownership is readable and explicit
- deploy ordering respects the DNS guard
- comments/docs match actual behavior

## Non-goals

This phase does not redesign TLS or proxy behavior.

It only establishes hard subsystem boundaries so the next phases can be implemented cleanly.
