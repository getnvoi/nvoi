# Phase 2: Collapse Ingress to One Runtime Model

## Goal

Make baseline ingress boring by replacing the current pile of booleans and exceptions with one explicit runtime model.

At the end of this phase:

- there is one baseline way to reason about ingress behavior
- firewall checks follow exposure mode
- TLS source does not create safety bypasses
- `IngressApply` reads like a simple state machine, not an accumulation of special cases
- transitions between ingress states are explicit rather than hidden magic

## Why this phase exists

Current ingress behavior is spread across:

- `Proxy` route flag
- no cert vs provided cert
- auto-generated Cloudflare Origin CA cert
- ACME assumptions
- explicit exceptions such as "provided cert skips coherence checks"

That means the tool has the right concepts, but the execution path is not coherent.

The specific regression to eliminate here:

- custom certs can bypass firewall coherence validation
- proxy behavior and TLS behavior are entangled in ad hoc conditionals

The deeper issue is architectural:

- Cloudflare-specific behavior is currently mixed into the baseline ingress model
- baseline ingress should stay generic
- provider-specific edge behavior should sit on top, not inside the baseline mental model

This phase should also define a reusable ingress boundary:

- ingress may inspect DNS and firewall state
- ingress must not mutate DNS or firewall state
- ingress fails hard when those prerequisites are incompatible

## Boundary summary for this phase

- ingress owns route and TLS application only
- ingress may read DNS state for guards
- ingress may read firewall state for guards
- ingress must not create or delete DNS records
- ingress must not reconcile firewall rules
- guard failures must point the user back to the DNS or firewall owner path
- baseline ingress concepts must stay generic rather than Cloudflare-specific

## Scope

Primary files:

- `pkg/core/dns.go`
- `pkg/kube/caddy.go`
- related tests in `pkg/core/dns_test.go`
- any helper types supporting route/TLS resolution

## Required model

Introduce explicit internal concepts for baseline ingress:

- exposure mode
  - `direct`
  - `edge_proxied`
- TLS mode
  - `acme`
  - `provided`
  - `edge_origin`

These do not need to be public config names yet. They are internal execution concepts.

The important part is that every baseline ingress deployment resolves to one valid combination instead of branching all over the function.

At this phase, "edge" should be treated as a generic overlay slot.
Cloudflare may be the first implementation of that slot, but the baseline model should not be named around Cloudflare itself.

## Required changes

### 1. Refactor `IngressApply` around resolved modes

In `pkg/core/dns.go`:

- split route parsing/collection from runtime mode resolution
- compute exposure mode from route proxy settings
- compute TLS mode from:
  - provided cert/key
  - edge proxy mode
  - direct mode

Then structure `IngressApply` like:

1. collect desired routes
2. resolve ingress modes
3. validate firewall coherence for those modes
4. resolve/provision TLS material
5. render/apply Caddy
6. verify deployment

That flow should be obvious from the code.

It must also handle transitions explicitly:

- direct -> edge-proxied
- edge-proxied -> direct
- ACME -> provided cert
- provided cert -> ACME
- ingress present -> ingress absent
- ingress absent -> ingress present

No transition should depend on an accidental side effect or a hidden control path.

### 2. Remove the custom-cert coherence bypass

This is mandatory in this phase.

Today the code skips `checkFirewallCoherence` when `CertPEM/KeyPEM` exist.
That must be removed.

Correct rule:

- cert source changes TLS handling
- cert source does not change required exposure safety

Meaning:

- direct + provided cert still requires direct/public firewall shape
- proxied + provided cert still requires Cloudflare-restricted firewall shape

### 3. Rewrite coherence checks against baseline exposure mode

`checkFirewallCoherence` should validate:

- `direct`
  - ports `80/443` available publicly
  - firewall matches direct/public exposure
- `edge_proxied`
  - ports `80/443` present
  - firewall matches the active edge-provider restriction model

It should not care whether TLS comes from:

- ACME
- provided cert
- edge-origin helper material

except where verification behavior differs.

Provider-specific details such as "Cloudflare CIDRs" should be implemented behind the edge overlay, not treated as the baseline concept itself.

### 4. Normalize verification behavior

The success/wait logic at the end of `IngressApply` should be aligned with the same model.

Examples:

- direct + ACME: wait for public HTTPS readiness
- direct + provided: still verify public HTTPS readiness
- proxied + provided/origin: verify according to proxied expectations, but do not pretend "success" before the safety model is satisfied

Avoid optimistic success messages that skip meaningful validation.

The same rule applies to adjacent systems:

- missing DNS is a validation failure, not something ingress silently creates
- incompatible firewall is a validation failure, not something ingress silently rewrites

### 5. Keep baseline rendering separate from overlay specifics

In `pkg/kube/caddy.go` and related rendering paths:

- baseline route rendering should remain generic
- provider-specific edge requirements should be injected through resolved mode/material, not by baking Cloudflare assumptions all over rendering
- keep the baseline readable even if Cloudflare is removed

### 6. Ensure Caddy generation remains a pure rendering step

In `pkg/kube/caddy.go`:

- keep route rendering simple
- keep TLS rendering derived from resolved route mode, not extra hidden assumptions
- avoid reintroducing policy decisions into Caddy generation

Policy belongs in `pkg/core`, rendering belongs in `pkg/kube`.

### 7. Make ingress absence a first-class reconciled state

The system must not rely on vague semantics like "empty routes means maybe delete".

This phase should make it explicit that:

- ingress present is a desired state
- ingress absent is also a desired state
- reconciliation between those states is intentional and testable

If empty routes remain the implementation mechanism, the model still needs to document and enforce ingress absence as a real state, not a hidden trick.

## Boundary checks to close in this phase

- `ingress apply` never writes DNS state
- `ingress apply` never writes firewall state
- all ingress-to-DNS and ingress-to-firewall interactions are read-only validation
- no ingress code path contains "helpful" neighboring subsystem mutation
- baseline ingress concepts remain generic and do not become Cloudflare-specific API
- ingress transitions and ingress absence do not create frozen or implicit behavior

## Tests to add or update

Add/update tests proving:

- provided cert does not skip firewall coherence
- proxied + open firewall fails
- direct + restricted firewall fails
- ACME/direct still works under public firewall assumptions
- mode resolution is deterministic for the supported combinations
- direct/proxied transitions reconcile cleanly
- ingress removal is an explicit and tested desired-state transition

If the current tests only assert happy paths, extend them to assert rejected combinations.

## Completion criteria

This phase is complete only when:

- ingress behavior can be described from two internal concepts: exposure mode and TLS mode
- no cert path bypasses safety checks
- `IngressApply` is mechanically readable
- Caddy generation is policy-free rendering
- baseline ingress still makes sense without Cloudflare present

## Non-goals

This phase does not yet decide whether Cloudflare-specific overlay features stay in their current form.

It prepares the code so that those decisions can be made cleanly in phase 3.
