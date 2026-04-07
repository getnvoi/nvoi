# Phase 5: Provider Parity, Tests, and Final Deletion of Clutter

## Goal

Close the cleanup completely.

This phase exists to make sure the boring model is true:

- across providers
- across tests
- across docs
- across leftover transitional code

No "we'll clean that later" residue should remain after this phase.

## Why this phase exists

Even if phases 1-4 are done correctly, the branch can still feel messy if:

- provider behavior differs subtly
- tests only cover happy paths
- docs describe exceptions
- compatibility code from the refactor remains in place

This is the phase that makes the cleanup final.

It should also confirm the boundary model holds at provider level:

- providers implement exposure semantics only
- providers do not smuggle extra behavior into higher-level deployment logic
- tests prove guards and ownership rules, not just successful apply flows

Cloudflare-specific provider concerns are allowed to live only in:

- `pkg/provider/cloudflare/`
- explicit overlay-facing provider integration points
- tests/docs that are specifically about the Cloudflare overlay

Cloudflare-specific provider concerns must not define:

- baseline deployment semantics
- generic ingress semantics
- generic firewall semantics
- generic TLS ownership semantics
- generic planner/runtime behavior outside explicit overlay paths

## Boundary summary for this phase

- provider layers own provider reconciliation only
- provider code must not leak deploy policy across subsystem lines
- provider parity means the same abstract boundary model is true on every backend
- tests must lock that in explicitly
- Cloudflare provider code must remain overlay/provider-scoped and must not become baseline behavioral gravity

## Scope

Primary files:

- `pkg/provider/allowlist.go`
- `pkg/provider/aws/firewall.go`
- `pkg/provider/hetzner/firewall.go`
- `pkg/provider/scaleway/firewall.go`
- provider tests
- deploy/ingress/firewall tests
- examples and docs

## Required changes

### 1. Normalize firewall semantics across providers

The firewall abstraction must mean the same thing everywhere.

Review and align:

- base rule behavior
- public port reconciliation behavior
- not-found behavior
- SSH defaults
- public/internal port treatment

If AWS/Hetzner/Scaleway differ meaningfully, fix them now.

This includes ensuring Cloudflare-related provider behavior does not redefine the generic abstraction instead of implementing one explicit overlay/provider case.

### 2. Fix Cloudflare preset parity

Current concern:

- default preset is dual-stack
- Cloudflare preset handling appears IPv4-only

If the product is otherwise dual-stack-aware, that mismatch must be fixed.

Do not keep a "safe preset" that is only partially safe/consistent.

Do not let Cloudflare-specific preset behavior leak into the generic model as if Cloudflare were the baseline reference provider.

### 3. Rebuild tests around invariants

The final test suite must prove the boring model, not just the feature additions.

Minimum invariant coverage:

- DNS delete does not touch ingress
- ingress is reconciled only by ingress/deploy logic
- proxied deploy with open firewall fails
- direct deploy with restricted firewall fails
- provided cert does not bypass coherence checks
- CLI and deploy ingress paths behave the same
- Origin CA behavior is fully owned, or absent
- provider firewall presets produce consistent semantics

### 4. Rewrite examples and help text

Examples must show the clean model only.

Remove or rewrite examples that encourage:

- CLI-only production behavior
- ambiguous proxy/TLS usage
- hidden assumptions about DNS or firewall side effects

The docs should answer "how does this deploy tool behave?" with one story.

### 5. Delete transitional code and stale comments

Final pass:

- remove dead helpers left from old/new model overlap
- remove comments claiming separation where special cases still existed
- remove compatibility branches that existed only to bridge the refactor
- rename anything still reflecting the old muddled model

This is essential. A boring system should not carry archaeological layers in the code.

## Boundary checks to close in this phase

- provider implementations stay narrow and do not absorb higher-level logic
- preset behavior does not undermine global safety boundaries
- tests prove provider behavior respects subsystem ownership
- docs/examples do not reintroduce cross-subsystem confusion
- Cloudflare provider behavior remains confined to provider/overlay scope and does not shape the baseline model

## Completion criteria

This phase is complete only when:

- provider behavior matches the same mental model
- tests lock in the desired invariants
- docs/examples teach one obvious path
- dead transitional logic is gone
- Cloudflare-specific provider behavior does not leak beyond explicit overlay/provider boundaries

## Final quality bar

After this phase, the branch should feel like:

- a tool with a few strong concepts
- implemented directly
- with no clever leftovers

That is the boring target.
