# Cleanup Overview

## Primary Goal

This cleanup is not a polish pass.

It is a forced return to a boring, trustworthy deployment tool.

The branch introduced real improvements, but implemented them in a way that left the system in a dangerous middle ground:

- more concepts
- more branches
- more exceptions
- weaker ownership
- less predictable behavior

That state is not acceptable.

The outcome of this cleanup must be a system that is:

- explicit
- minimal in behavior
- strict in ownership
- predictable under failure
- easy to explain in a few sentences

If a feature cannot be explained simply, owned clearly, and enforced consistently, it does not stay in its current shape.

## Baseline vs Overlay

This cleanup must also restore a clean product layering:

- baseline deployment behavior
- optional edge/distribution overlays

Baseline is the core deployment tool. It must remain boring and fully useful on its own:

- compute lifecycle
- service lifecycle
- firewall exposure
- DNS records
- ingress routes
- TLS handling

That baseline must work cleanly for:

- ordinary cloud deploys
- local/private environments
- future edge providers

Cloudflare must not be treated as part of the baseline mental model.

Cloudflare should instead feel like an explicit overlay that comes on top of the baseline:

- proxied edge delivery
- Origin CA helpers
- tunnels
- Access protection
- future Cloudflare-specific edge capabilities

That means:

- the baseline model stays provider-agnostic
- Cloudflare-specific behavior is attached explicitly when requested
- Cloudflare does not reshape baseline semantics everywhere in the code

## What success looks like

After this cleanup, the tool must feel boring in the best possible way:

- `deploy` is the canonical production path
- DNS only manages DNS
- ingress only manages Caddy/routes/TLS
- firewall only manages exposure
- baseline exposure is generic and Cloudflare-free by default
- edge/distribution behavior is layered on top explicitly
- custom certs are either fully owned or not supported in that form
- no command mutates another subsystem behind the user’s back
- no TLS path bypasses safety checks
- no provider behaves meaningfully differently from the others

The user should be able to read the config and predict exactly:

- what gets exposed
- how traffic reaches the app
- where TLS comes from
- what safety checks are enforced

without needing to know hidden exceptions in the code.

## What this cleanup is explicitly rejecting

This cleanup rejects the current half-finished state where:

- DNS and ingress are said to be separate, but DNS still edits ingress
- custom certs and proxy behavior interact through exceptions
- Cloudflare-specific behavior exists without complete operational ownership
- Cloudflare concepts are interwoven into the baseline model instead of layered on top
- production behavior differs between deploy config and imperative CLI paths
- abstractions exist, but execution still depends on hidden coupling

That is clutter, not architecture.

## Non-negotiable standard

Every phase must move the system toward one boring model:

- one owner per subsystem
- one baseline runtime model for ingress/exposure
- one explicit overlay model for edge/provider-specific capabilities
- one production truth
- one consistent safety model

No phase is complete if it leaves behind transitional logic, side effects, or "we will clean that later" complexity.

## Phase Order

The phases must be executed in this order:

1. Phase 1: enforce hard subsystem boundaries
2. Phase 2: collapse ingress into one runtime model
3. Phase 3: finish TLS ownership decisively
4. Phase 4: make declarative deploy the only production truth
5. Phase 5: align providers, tests, docs, and delete all remaining clutter
6. Phase 6: extract and harden the optional edge/distribution overlay
7. Phase 7: enforce the remaining cross-subsystem guard boundaries
8. Final check: verify the whole system against the end-state assertions

Do not skip ahead.

If boundaries are not fixed first, later phases will just repackage the same confusion in cleaner words.
