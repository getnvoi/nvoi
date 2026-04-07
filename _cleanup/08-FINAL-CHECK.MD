- [ ] DNS commands mutate DNS only.
Check `pkg/core/dns.go`, `internal/core/dns.go`, and DNS-related plan/executor paths for any SSH, kubectl, Caddy, or ingress side effects.

- [ ] Ingress commands mutate ingress only.
Check `pkg/core/dns.go` ingress logic, `internal/core/ingress.go`, and `pkg/kube/caddy.go` for any DNS provider writes, firewall mutation, or unrelated deploy behavior.

- [ ] Firewall commands mutate exposure only.
Check `pkg/core/firewall.go`, `internal/core/firewall.go`, and provider firewall implementations under `pkg/provider/*/firewall.go` for any DNS, ingress, or workload side effects.

- [ ] Deploy is the only canonical production path.
Check `internal/api/config/schema.go`, `internal/api/plan/plan.go`, `internal/api/handlers/executor.go`, and CLI wrappers under `internal/core/` to confirm production behavior is fully expressible and executed through deploy semantics.

- [ ] No supported production behavior exists only behind CLI flags.
Check `internal/core/ingress.go`, `internal/core/dns.go`, and config/API files in `internal/api/config/` for flag-only production features, especially TLS and proxy behavior.

- [ ] Proxy mode has one runtime meaning.
Check mode resolution and enforcement in `pkg/core/dns.go`, route rendering in `pkg/kube/caddy.go`, and config validation in `internal/api/config/validate.go`.

- [ ] Custom certs do not bypass safety checks.
Check `pkg/core/dns.go` around ingress apply, coherence checks, and TLS handling to confirm provided cert paths still enforce the correct firewall/exposure model.

- [ ] Every supported TLS mode has clear ownership.
Check ingress/TLS logic in `pkg/core/dns.go`, cert storage in `pkg/kube/caddy.go`, config/API representation in `internal/api/config/`, and any CLI-only TLS behavior in `internal/core/ingress.go`.

- [ ] Origin CA is fully owned or absent.
Check `pkg/provider/cloudflare/origin_ca.go`, proxied ingress flow in `pkg/core/dns.go`, related tests, and any storage/reuse/revocation logic to confirm there is no half-managed automation.

- [ ] Provider firewall behavior matches one consistent model.
Check `pkg/provider/allowlist.go` and provider implementations in `pkg/provider/aws/firewall.go`, `pkg/provider/hetzner/firewall.go`, and `pkg/provider/scaleway/firewall.go`, including tests.

- [ ] The config fully explains production behavior.
Check `internal/api/config/schema.go`, `internal/api/config/validate.go`, examples, and any deploy-facing docs to confirm users can infer exposure, proxy, and TLS behavior from config alone.

- [ ] The code contains no hidden cross-subsystem side effects.
Check plan/executor/runtime layers in `internal/api/plan/plan.go`, `internal/api/handlers/executor.go`, `pkg/core/`, and command wrappers in `internal/core/` for unexpected writes outside the owning subsystem.

- [ ] The docs describe one obvious workflow.
Check examples, command help strings, and any markdown/docs folders for duplicate or conflicting production paths, especially around ingress, proxy, and custom certs.

- [ ] No transitional clutter remains.
Check comments, dead helpers, compatibility branches, and duplicated logic across `internal/` and `pkg/` for leftover refactor scaffolding that no longer belongs in the final model.

- [ ] Baseline deployment remains coherent without Cloudflare.
Check `internal/api/config/`, `internal/api/plan/`, and `pkg/core/` to confirm the core model still makes sense if Cloudflare-specific behavior is mentally removed.

- [ ] Cloudflare reads as an optional overlay, not a baseline assumption.
Check config shape, runtime mode resolution, provider integration, and docs/examples for whether Cloudflare feels additive rather than interwoven.

## Boundary Recap

- [ ] `dns set` manages DNS only and does not imply ingress creation.
Check DNS command/runtime paths in `internal/core/dns.go` and `pkg/core/dns.go` for any route creation or ingress side effects.

- [ ] `dns delete` manages DNS only, and fails if ingress still references the domain.
Check guarded deletion behavior in `pkg/core/dns.go`, any route inspection helpers, and deploy ordering in `internal/api/plan/plan.go`.

- [ ] `ingress apply` manages ingress only and does not mutate DNS.
Check ingress runtime and CLI wrapper paths in `pkg/core/dns.go` and `internal/core/ingress.go` for DNS provider writes.

- [ ] `ingress apply` manages ingress only and does not mutate firewall.
Check ingress runtime logic in `pkg/core/dns.go` for read-only firewall validation only, with no firewall reconciliation calls.

- [ ] `firewall set` manages exposure only and does not mutate DNS.
Check `pkg/core/firewall.go`, `internal/core/firewall.go`, and provider firewall paths for any DNS provider usage or record mutation.

- [ ] `firewall set` manages exposure only and does not mutate ingress.
Check firewall flows for any Caddy, kubectl, route, or ingress reconciliation behavior beyond validation guards.

- [ ] `service delete` does not silently remove ingress or DNS dependencies.
Check deletion flow in `pkg/core/service.go`, executor/plan ordering, and any guard logic for active ingress references.

- [ ] ingress reconciliation does not silently delete DNS.
Check ingress/deploy reconciliation paths in `pkg/core/dns.go` and `internal/api/plan/plan.go` for any automatic DNS cleanup tied to route removal.

- [ ] neighboring subsystems are inspected for guards but never mutated implicitly.
Check all guard-style validations across `pkg/core/`, `internal/api/plan/`, and `internal/core/` to confirm the pattern is inspect, reject, instruct.

- [ ] edge/distribution behavior remains an explicit overlay.
Check config, runtime, provider integration, and docs to confirm Cloudflare-specific capabilities do not redefine the baseline model.
