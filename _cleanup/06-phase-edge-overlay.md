# Phase 6: Extract the Edge Overlay

## Goal

Make the layering explicit and durable:

- baseline deployment stays generic and boring
- edge/distribution behavior stays optional
- Cloudflare feels attached on top, not woven through the core

This phase exists because the previous phases can clean up behavior without fully preventing Cloudflare-specific concepts from drifting back into the baseline model.

## What this phase must lock in

- baseline concepts remain provider-agnostic
- edge/distribution concepts are modeled explicitly as overlays
- Cloudflare is one overlay implementation, not the baseline mental model
- future edge capabilities such as tunnels or Access can be added without reshaping core deployment behavior

## Boundary summary for this phase

- baseline deployment owns compute, services, firewall exposure, DNS, ingress, and baseline TLS behavior
- edge overlays may build on those primitives, but do not redefine them
- Cloudflare-specific behavior must activate only when explicitly requested
- overlay code may depend on baseline state, but baseline code must remain understandable without overlay assumptions

## Required changes

### 1. Make baseline vs overlay visible in the product model

In config/runtime/docs:

- baseline exposure and ingress concepts should read generically
- edge-specific behavior should be attached explicitly
- avoid Cloudflare-specific names and assumptions in the baseline model unless absolutely unavoidable

### 2. Isolate Cloudflare-specific capability paths

Review Cloudflare-related behavior and ensure it is clearly treated as overlay logic:

- proxied edge delivery
- Origin CA helpers
- future tunnel support
- future Access support

This does not mean all future features must be implemented now.
It means the structure should make them possible without contaminating the baseline.

### 3. Prevent overlay logic from redefining baseline semantics

Examples of what to avoid:

- baseline ingress meaning changing because Cloudflare exists
- baseline TLS ownership becoming Cloudflare-shaped
- baseline firewall concepts being named around Cloudflare rather than generic edge restrictions

### 4. Keep the baseline fully valid without the overlay

At the end of this phase, the tool should still make perfect sense if Cloudflare support were mentally removed.

That is the test:

- would the baseline still feel coherent, complete, and boring?

If not, the overlay is still too interwoven.

### 5. Ensure overlay removal is as clean as overlay enablement

The overlay model must not only be attachable; it must also be removable without leaving dead behavior behind.

Examples:

- removing edge proxying should return the system to a clean baseline exposure model
- removing edge TLS helpers should not leave baseline TLS semantics ambiguous
- future tunnel or Access support should be removable without forcing baseline cleanup through hidden side effects

## Areas to check

- `internal/api/config/`
- `internal/api/plan/`
- `pkg/core/`
- `pkg/provider/cloudflare/`
- examples/docs/help text

## Completion criteria

- Cloudflare clearly reads as an optional overlay
- baseline deployment remains provider-agnostic
- baseline concepts do not carry Cloudflare-specific gravity
- future edge features can be added without reworking core deployment semantics
- enabling and removing overlay behavior both reconcile cleanly
