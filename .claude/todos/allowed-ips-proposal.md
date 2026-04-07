# Firewall Set — Implementation Proposal

Dedicated `firewall set` command. Single owner of all public-facing firewall rules.
Replaces the earlier `--allowed-ips` on `instance set` / `dns set` approach.

## Philosophy

- **`instance set` creates the firewall.** Base rules only: SSH open + internal ports. Never manages HTTP ports.
- **`firewall set` controls public ports.** Explicit, declarative, idempotent. Omitted ports are closed.
- **`dns set` never touches the firewall.** DNS + Caddy only.
- **Internal ports are always present.** 6443, 10250, 8472, 5000 — the user never specifies these.
- **No `firewall` config + `domains` config = hard error.** "domains configured but no firewall rules for 80/443."

## CLI UX

### Presets

Named presets resolve to a `PortAllowList` at runtime. Raw args can layer on top —
same port in both = raw arg wins.

SSH (port 22) is always open. It's infrastructure — nvoi can't operate without it.
Managed by `instance set`, not configurable via `firewall set`. Same as 6443/10250/8472/5000.

`firewall set` controls HTTP ports (80, 443) and any custom ports.

```bash
# Presets
nvoi firewall set default                         # 80:0.0.0.0/0 443:0.0.0.0/0
nvoi firewall set cloudflare                       # 80:<live CF IPs> 443:<live CF IPs>

# Raw rules (full control)
nvoi firewall set 80:0.0.0.0/0 443:0.0.0.0/0
nvoi firewall set 80:10.0.0.0/8 443:10.0.0.0/8

# Preset + override (merge — raw wins for same port)
nvoi firewall set cloudflare 443:0.0.0.0/0         # 80 from CF preset, 443 override to open

# Close HTTP entirely (SSH + internal still active)
nvoi firewall set

# Custom ports
nvoi firewall set 80:0.0.0.0/0 443:0.0.0.0/0 8080:10.0.0.0/8

# Show current rules
nvoi firewall list
```

### Preset definitions

| Preset | Resolves to |
|--------|-------------|
| `default` | `80:0.0.0.0/0 443:0.0.0.0/0` |
| `cloudflare` | `80:<CF IPs> 443:<CF IPs>` — fetched live from `https://api.cloudflare.com/client/v4/ips` |

SSH is not in presets — it's always open, managed by `instance set`.

`cloudflare` preset fetches Cloudflare's published IP ranges at runtime via their
public API. Falls back to a hardcoded list if the fetch fails (offline deploys).
The response is ~15 stable IPv4 CIDRs.

### Merge semantics

When a preset and raw args are mixed, raw args override the preset for the same port.
Ports only in the preset are kept. Ports only in raw args are added.

```bash
nvoi firewall set cloudflare 443:0.0.0.0/0
# → 80:<CF IPs>   (from preset, no raw override)
# → 443:0.0.0.0/0 (raw wins over preset's CF IPs)
```

### Format

`port:cidr,cidr`. Positional args. CIDRs normalized to `/32` if bare IP.

Env var fallback:

```bash
NVOI_FIREWALL="default"                              # preset
NVOI_FIREWALL="cloudflare;22:1.2.3.4/32"             # preset + override
NVOI_FIREWALL="22:0.0.0.0/0;80:0.0.0.0/0;443:0.0.0.0/0"  # raw
```

## Deploy sequence

```bash
nvoi instance set master --compute-type cx23 --compute-region fsn1   # 1. server + base firewall
nvoi firewall set default                                             # 2. open public ports
nvoi service set web --image myapp --port 3000                        # 3. deploy service
nvoi dns set web example.com                                          # 4. DNS + Caddy
```

## What the firewall looks like

After `instance set` (base rules only):

```
22    → 0.0.0.0/0     (SSH — default open)
6443  → 10.0.0.0/16   (k8s API)
10250 → 10.0.0.0/16   (kubelet)
8472  → 10.0.0.0/16   (flannel VXLAN)
5000  → 10.0.0.0/16   (registry)
```

After `firewall set 22:203.0.113.50/32 80:0.0.0.0/0 443:0.0.0.0/0`:

```
22    → 203.0.113.50/32  (SSH restricted)
80    → 0.0.0.0/0        (HTTP open)
443   → 0.0.0.0/0        (HTTPS open)
6443  → 10.0.0.0/16      (unchanged)
10250 → 10.0.0.0/16      (unchanged)
8472  → 10.0.0.0/16      (unchanged)
5000  → 10.0.0.0/16      (unchanged)
```

After `firewall set 22:0.0.0.0/0` (remove HTTP):

```
22    → 0.0.0.0/0        (SSH open)
6443  → 10.0.0.0/16      (unchanged)
...internal unchanged
```

80/443 gone. Omitted = closed.

## Cloudflare proxy mode (`--proxy`)

**Cloudflare only.** `--proxy` on `dns set` creates a proxied DNS record (`proxied: true`).
Cloudflare terminates TLS at the edge, forwards to origin. Hard error if dns provider
is not cloudflare — proxied records are a Cloudflare-specific concept, no other DNS
provider supports it.

### CLI

```bash
# Proxied — Cloudflare terminates TLS, origin receives plain HTTP
nvoi dns set web example.com --proxy

# Not proxied — Caddy does TLS via ACME (current default)
nvoi dns set web example.com
```

`--proxy` is Cloudflare-specific. Requires `--dns-provider cloudflare` (or `DNS_PROVIDER=cloudflare`).
Any other provider → hard error:

```
--proxy requires Cloudflare as DNS provider (current: aws)
```

### Effect on Caddy

`--proxy` changes how the Caddyfile is generated. Stored on `IngressRoute.Proxy bool`.

**Without `--proxy` (current)** — Caddy does TLS, ACME:

```
example.com {
    reverse_proxy web.ns.svc.cluster.local:3000
}
```

**With `--proxy`** — plain HTTP, no TLS, no ACME:

```
:80 {
    @web host example.com
    reverse_proxy @web web.ns.svc.cluster.local:3000
}
```

All proxied routes share a single `:80` block with host matchers.
Non-proxied routes keep individual domain blocks with auto-TLS.
Both can coexist in the same Caddyfile (Caddy handles `:80` and `:443` simultaneously).

### Cross-validation: firewall × proxy coherence

Incoherent configurations are hard errors. These are not warnings — they must not deploy.

**`firewall: cloudflare` + domain without `--proxy`:**

```
firewall restricts 80/443 to Cloudflare IPs but domain "example.com" is not proxied —
ACME will fail (Let's Encrypt validators are not Cloudflare).
Add --proxy to dns set, or change firewall to "default".
```

**`firewall: default` (or explicit `80:0.0.0.0/0`) + domain with `--proxy`:**

```
domain "example.com" is proxied through Cloudflare but firewall is open to all —
origin is directly reachable, bypassing Cloudflare proxy.
Use "firewall set cloudflare" to restrict 80/443 to Cloudflare IPs, or remove --proxy.
```

**dns provider is not cloudflare + `--proxy`:**

```
--proxy requires Cloudflare as DNS provider (current: aws)
```

### API / SaaS config YAML — proxy on domains

`proxy: true` on structured domain config. Only valid when `dns_provider: cloudflare`.

```yaml
# Simple (no proxy — current behavior)
domains:
  web: example.com

# Proxied (Cloudflare only)
domains:
  web:
    domains: [example.com]
    proxy: true
```

Validation in `plan.Build()`:
- `proxy: true` + `dns_provider` is not `cloudflare` → error
- `proxy: true` + `firewall` is not `cloudflare` preset and doesn't restrict 80/443 to CF IPs → error
- `proxy: false` + `firewall` is `cloudflare` preset → error

### The natural pairing

`firewall set cloudflare` and `dns set --proxy` go together. They form a coherent
Cloudflare-first security posture:

```bash
nvoi instance set master --compute-type cx23 --compute-region fsn1
nvoi firewall set cloudflare              # 80/443 → CF IPs only
nvoi service set web --image myapp --port 3000
nvoi dns set web example.com --proxy      # CF terminates TLS, origin plain HTTP
```

No ACME. No Let's Encrypt. No port 80 open to the world. Caddy is a plain HTTP
reverse proxy behind Cloudflare's edge. Firewall only accepts CF traffic.

## API / SaaS config YAML

Supports presets (string) or explicit rules (map), or preset + overrides (map with `preset` key):

```yaml
# Preset — simplest
firewall: default

# Preset — Cloudflare
firewall: cloudflare

# Preset + override
firewall:
  preset: cloudflare
  443: [0.0.0.0/0]               # override 443 from preset, keep 80 as CF IPs

# Explicit rules — full control
firewall:
  80: [0.0.0.0/0]
  443: [0.0.0.0/0]
```

Full config example:

```yaml
servers:
  master:
    type: cx23
    region: fsn1

firewall: default                   # or cloudflare, or explicit map

services:
  web:
    image: myapp
    port: 3000

domains:
  web: example.com
```

No `firewall` section = base rules only (SSH + internal). If `domains` is set
but `firewall` doesn't include 80 or 443, `plan.Build()` returns an error:

```
firewall: domains configured but port 80 not open — add "firewall: default" or explicit 80/443 rules
```

## Data Structures

### `PortAllowList` (unchanged from earlier thinking)

**File:** `pkg/provider/compute.go`

```go
// PortAllowList maps port strings to allowed source CIDRs.
// Ports present override defaults. Ports absent are closed (for public ports)
// or keep provider defaults (for internal ports).
// A nil map = base rules only (SSH open + internal).
type PortAllowList map[string][]string
```

### Parse + preset resolution

**File:** `pkg/provider/allowlist.go` (new)

```go
// ResolveFirewallArgs parses CLI args into a PortAllowList.
// First arg may be a preset name ("default", "cloudflare").
// Remaining args are raw "port:cidr,cidr" rules that override the preset.
// SSH (22) is never included — it's always open, managed by instance set.
func ResolveFirewallArgs(ctx context.Context, args []string) (PortAllowList, error) {
    if len(args) == 0 {
        return nil, nil
    }

    var base PortAllowList
    rawArgs := args

    // Check if first arg is a preset name (no ":" = not a raw rule)
    if len(args) > 0 && !strings.Contains(args[0], ":") {
        preset, err := resolvePreset(ctx, args[0])
        if err != nil {
            return nil, err
        }
        base = preset
        rawArgs = args[1:]
    }

    // Parse raw rules
    overrides := parseRawRules(rawArgs)

    // Merge: raw overrides win for same port
    return mergeAllowLists(base, overrides), nil
}

// resolvePreset returns the PortAllowList for a named preset.
// SSH (22) is NOT included — always open, managed separately by instance set.
func resolvePreset(ctx context.Context, name string) (PortAllowList, error) {
    switch name {
    case "default":
        return PortAllowList{
            "80":  {"0.0.0.0/0", "::/0"},
            "443": {"0.0.0.0/0", "::/0"},
        }, nil

    case "cloudflare":
        cidrs, err := fetchCloudflareIPs(ctx)
        if err != nil {
            cidrs = fallbackCloudflareIPs  // hardcoded fallback
        }
        return PortAllowList{
            "80":  cidrs,
            "443": cidrs,
        }, nil

    default:
        return nil, fmt.Errorf("unknown firewall preset: %q (available: default, cloudflare)", name)
    }
}

// fetchCloudflareIPs fetches Cloudflare's published IP ranges.
// GET https://api.cloudflare.com/client/v4/ips → {"result": {"ipv4_cidrs": [...]}}
func fetchCloudflareIPs(ctx context.Context) ([]string, error) {
    // standard HTTP GET, parse JSON, return ipv4_cidrs
}

// fallbackCloudflareIPs is used when the API fetch fails (offline deploys).
var fallbackCloudflareIPs = []string{
    "173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22",
    "103.31.4.0/22", "141.101.64.0/18", "108.162.192.0/18",
    "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22",
    "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
    "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
}

// mergeAllowLists merges base + overrides. Override wins for same port.
func mergeAllowLists(base, overrides PortAllowList) PortAllowList {
    if base == nil && overrides == nil {
        return nil
    }
    result := PortAllowList{}
    for port, ips := range base {
        result[port] = ips
    }
    for port, ips := range overrides {
        result[port] = ips  // override wins
    }
    if len(result) == 0 {
        return nil
    }
    return result
}

// parseRawRules parses "port:cidr,cidr" args (no preset handling).
func parseRawRules(args []string) PortAllowList {
    result := PortAllowList{}
    for _, arg := range args {
        for _, group := range strings.Split(arg, ";") {
            group = strings.TrimSpace(group)
            port, cidrs, ok := strings.Cut(group, ":")
            if !ok || port == "" {
                continue
            }
            for _, cidr := range strings.Split(cidrs, ",") {
                cidr = strings.TrimSpace(cidr)
                if cidr == "" {
                    continue
                }
                if !strings.Contains(cidr, "/") {
                    cidr += "/32"
                }
                result[port] = append(result[port], cidr)
            }
        }
    }
    if len(result) == 0 {
        return nil
    }
    return result
}
```

### New interface method on `ComputeProvider`

**File:** `pkg/provider/compute.go`

```go
type ComputeProvider interface {
    // ...existing methods...

    // ReconcileFirewallRules replaces public port rules on the named firewall.
    // Internal ports (6443, 10250, 8472, 5000) are always preserved.
    // Public ports not in the PortAllowList are removed.
    // SSH (22) defaults to 0.0.0.0/0 if not specified.
    ReconcileFirewallRules(ctx context.Context, name string, allowed PortAllowList) error

    // GetFirewallRules returns the current public port rules on the named firewall.
    GetFirewallRules(ctx context.Context, name string) (PortAllowList, error)
}
```

## Provider implementations

### Shared logic

Every provider builds the full rule set the same way:

```go
func buildFirewallRules(allowed PortAllowList) []Rule {
    rules := internalRules()  // 6443, 10250, 8472, 5000 — always present

    // SSH defaults to open if not explicitly specified
    sshCIDRs := []string{"0.0.0.0/0", "::/0"}
    if ips, ok := allowed["22"]; ok && len(ips) > 0 {
        sshCIDRs = ips
    }
    rules = append(rules, Rule{Port: "22", Protocol: "tcp", SourceIPs: sshCIDRs})

    // Public ports — only present if explicitly specified
    for _, port := range []string{"80", "443"} {
        if ips, ok := allowed[port]; ok && len(ips) > 0 {
            rules = append(rules, Rule{Port: port, Protocol: "tcp", SourceIPs: ips})
        }
    }

    // Custom ports — anything else in the allow list
    for port, ips := range allowed {
        if port == "22" || port == "80" || port == "443" {
            continue // already handled
        }
        if isInternalPort(port) {
            continue // never override internal
        }
        if len(ips) > 0 {
            rules = append(rules, Rule{Port: port, Protocol: "tcp", SourceIPs: ips})
        }
    }

    return rules
}
```

### Hetzner

`ReconcileFirewallRules`: find firewall by name → `POST /firewalls/{id}/actions/set_rules`
with `buildFirewallRules(allowed)`.

`GetFirewallRules`: find firewall by name → `GET /firewalls/{id}` → parse rules →
return public ports as `PortAllowList`.

### AWS

`ReconcileFirewallRules`: find SG by name → `RevokeSecurityGroupIngress` (all current) →
`AuthorizeSecurityGroupIngress` with `buildFirewallRules(allowed)`.

`GetFirewallRules`: find SG → `DescribeSecurityGroups` → parse `IpPermissions` →
return public ports as `PortAllowList`.

### Scaleway

`ReconcileFirewallRules`: find SG by name → delete all rules → add `buildFirewallRules(allowed)`.

`GetFirewallRules`: find SG → `GET /security_groups/{id}/rules` → parse →
return public ports as `PortAllowList`.

## Changes to `instance set`

**`defaultFirewallRules()` in all three providers changes:**

Remove 80/443 from the default set. The function becomes `baseFirewallRules()`:

```go
func baseFirewallRules() []Rule {
    pub := []string{"0.0.0.0/0", "::/0"}
    priv := []string{"10.0.0.0/16"}
    return []Rule{
        {Port: "22", Protocol: "tcp", SourceIPs: pub},      // SSH — open by default
        {Port: "6443", Protocol: "tcp", SourceIPs: priv},   // k8s API
        {Port: "10250", Protocol: "tcp", SourceIPs: priv},  // kubelet
        {Port: "8472", Protocol: "udp", SourceIPs: priv},   // flannel
        {Port: "5000", Protocol: "tcp", SourceIPs: priv},   // registry
    }
}
```

`ensureFirewall` on all three providers uses `baseFirewallRules()` — no HTTP ports.
`instance set` never calls `ReconcileFirewallRules` — it only creates the firewall
on first run and reconciles base rules.

## New command: `firewall set`

### pkg/core/firewall.go (new)

```go
type FirewallSetRequest struct {
    Cluster
    AllowedIPs provider.PortAllowList
}

func FirewallSet(ctx context.Context, req FirewallSetRequest) error {
    out := req.Log()
    names, err := req.Names()
    if err != nil {
        return err
    }
    prov, err := req.Compute()
    if err != nil {
        return err
    }

    out.Command("firewall", "set", names.Firewall())

    if err := prov.ReconcileFirewallRules(ctx, names.Firewall(), req.AllowedIPs); err != nil {
        return fmt.Errorf("firewall set: %w", err)
    }

    // Log what was set
    if req.AllowedIPs == nil || len(req.AllowedIPs) == 0 {
        out.Success("base rules only (SSH + internal)")
    } else {
        for port, ips := range req.AllowedIPs {
            out.Success(fmt.Sprintf("port %s → %v", port, ips))
        }
    }
    return nil
}
```

### pkg/core/firewall_list.go (or inline)

```go
type FirewallListRequest struct {
    Cluster
}

func FirewallList(ctx context.Context, req FirewallListRequest) (provider.PortAllowList, error) {
    names, err := req.Names()
    if err != nil {
        return nil, err
    }
    prov, err := req.Compute()
    if err != nil {
        return nil, err
    }
    return prov.GetFirewallRules(ctx, names.Firewall())
}
```

### internal/core/firewall.go (new — cobra commands)

```go
func newFirewallCmd() *cobra.Command {
    cmd := &cobra.Command{Use: "firewall", Short: "Manage firewall rules"}
    cmd.AddCommand(newFirewallSetCmd())
    cmd.AddCommand(newFirewallListCmd())
    return cmd
}

func newFirewallSetCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "set [preset] [port:cidr,cidr ...]",
        Short: "Set allowed IPs for public ports (omitted ports are closed)",
        Long: `Set firewall rules for public-facing ports. Internal ports are always preserved.

Presets: default, cloudflare, ssh-only
Raw rules: port:cidr,cidr (e.g. 22:0.0.0.0/0 80:10.0.0.0/8)
Mix: preset + raw overrides (raw wins for same port)`,
        Args:  cobra.ArbitraryArgs,
        RunE: func(cmd *cobra.Command, args []string) error {
            // ...resolve app, env, provider, creds...

            if len(args) == 0 {
                if envVal := os.Getenv("NVOI_FIREWALL"); envVal != "" {
                    args = strings.Split(envVal, ";")
                }
            }

            allowed, err := provider.ResolveFirewallArgs(cmd.Context(), args)
            if err != nil {
                return err
            }

            return app.FirewallSet(cmd.Context(), app.FirewallSetRequest{
                Cluster:    cluster,
                AllowedIPs: allowed,
            })
        },
    }
    addComputeProviderFlags(cmd)
    addAppFlags(cmd)
    return cmd
}
```

## API / SaaS plan integration

### Config schema

**File:** `internal/api/config/schema.go`

`Firewall` supports three forms in YAML: string preset, map with preset + overrides,
or explicit map. Custom `UnmarshalYAML`/`UnmarshalJSON` to handle all three.

```go
// FirewallConfig supports presets and/or explicit port rules.
//
//   firewall: default                    # string preset
//   firewall: cloudflare                 # string preset
//   firewall:                            # preset + overrides
//     preset: cloudflare
//     22: [1.2.3.4/32]
//   firewall:                            # explicit only
//     22: [0.0.0.0/0]
//     80: [0.0.0.0/0]
type FirewallConfig struct {
    Preset string              `json:"preset,omitempty" yaml:"preset,omitempty"`
    Rules  map[string][]string `json:"rules,omitempty" yaml:"rules,omitempty"` // port → CIDRs
}

type Config struct {
    Servers  map[string]Server  `json:"servers" yaml:"servers"`
    Firewall *FirewallConfig    `json:"firewall,omitempty" yaml:"firewall,omitempty"` // NEW
    Volumes  map[string]Volume  `json:"volumes,omitempty" yaml:"volumes,omitempty"`
    // ...rest unchanged
}
```

`FirewallConfig.UnmarshalYAML`: if scalar string → preset. If mapping with `preset` key →
preset + remaining keys are port overrides. If mapping without `preset` → explicit rules only.

### Validation

**File:** `internal/api/config/validate.go`

New rule: if `Domains` is non-empty but `Firewall` doesn't include port 80 or 443:

```go
if len(cfg.Domains) > 0 && len(cfg.Firewall) > 0 {
    has80 := len(cfg.Firewall["80"]) > 0
    has443 := len(cfg.Firewall["443"]) > 0
    if !has80 || !has443 {
        return fmt.Errorf("firewall: domains configured but ports 80/443 not open — add to firewall section")
    }
}
if len(cfg.Domains) > 0 && len(cfg.Firewall) == 0 {
    return fmt.Errorf("firewall: domains configured but no firewall section — add firewall with ports 80 and 443")
}
```

### Plan

**File:** `internal/api/plan/plan.go`

New step kind:

```go
StepFirewallSet StepKind = "firewall.set"
```

New set phase — runs after `instance.set`, before `volume.set`:

```go
func setFirewall(cfg *Cfg) []Step {
    if len(cfg.Firewall) == 0 {
        return nil
    }
    return []Step{{
        Kind:   StepFirewallSet,
        Name:   "firewall",
        Params: map[string]any{"rules": cfg.Firewall},
    }}
}
```

Deploy order becomes:

```
1. instance.set      (server + base firewall)
2. firewall.set      (public port rules)      ← NEW
3. volume.set
4. build
5. secret.set
6. storage.set
7. service.set
8. dns.set           (DNS + Caddy only — no firewall)
```

### Executor

**File:** `internal/api/handlers/executor.go`

```go
case plan.StepFirewallSet:
    rules := parseFirewallFromParams(params)
    return pkgcore.FirewallSet(ctx, pkgcore.FirewallSetRequest{
        Cluster:    e.cluster,
        AllowedIPs: rules,
    })
```

## What changes on `instance set` (per provider)

All three providers: rename `defaultFirewallRules()` → `baseFirewallRules()`, remove 80/443.

`ensureFirewall` keeps using `baseFirewallRules()` for create AND for reconcile-on-existing.
This means `instance set` on redeploy resets to base rules. BUT: `firewall.set` runs right
after in the API deploy sequence and restores the desired public ports. In the direct CLI,
the user runs `firewall set` explicitly after `instance set`.

The brief window between `instance set` resetting rules and `firewall set` restoring them
exists in the API path. For Hetzner (stateful firewall), existing TCP connections survive.
New connections to 80/443 fail during that window. Acceptable — it's seconds, and the
executor runs steps sequentially.

## Tests

### `pkg/provider/allowlist_test.go` (new)

- `TestParseRawRules` — multiple args parsed correctly
- `TestParseRawRules_EnvVar` — semicolon-separated format
- `TestParseRawRules_BareIPs` — normalized to /32
- `TestParseRawRules_Empty` — nil result
- `TestResolveFirewallArgs_PresetDefault` — "default" → 80+443 open (no SSH — managed by instance set)
- `TestResolveFirewallArgs_PresetCloudflare` — "cloudflare" → 80/443 CF IPs (needs httptest mock for CF API)
- `TestResolveFirewallArgs_PresetPlusOverride` — "cloudflare 443:0.0.0.0/0" → 80 from CF preset, 443 overridden
- `TestResolveFirewallArgs_UnknownPreset` — error
- `TestResolveFirewallArgs_RawOnly` — no preset, raw rules only
- `TestMergeAllowLists` — override wins for same port, both kept for different ports

### `pkg/core/firewall_test.go` (new)

- Test with mock compute provider
- Verify `ReconcileFirewallRules` called with correct `PortAllowList`

### Per-provider tests (extend)

- `TestBaseFirewallRules_NoHTTP` — verify 80/443 NOT in base rules
- `TestReconcileFirewallRules_AddsHTTP` — verify 80/443 added when in allow list
- `TestReconcileFirewallRules_ClosesHTTP` — verify 80/443 removed when not in allow list
- `TestReconcileFirewallRules_PreservesInternal` — verify 6443 etc always present

### `internal/api/config/validate_test.go` (extend)

- `TestValidate_DomainsWithoutFirewall` — error
- `TestValidate_DomainsWithoutPort80` — error
- `TestValidate_DomainsWithFirewall` — pass
- `TestValidate_NoDomainsNoFirewall` — pass
- `TestValidate_ProxyWithoutCloudflare` — error (proxy: true but dns_provider != cloudflare)
- `TestValidate_ProxyWithDefaultFirewall` — error (proxy: true but firewall open to all)
- `TestValidate_CloudflareFirewallWithoutProxy` — error (firewall cloudflare but domain not proxied)
- `TestValidate_CloudflareFirewallWithProxy` — pass

### `internal/api/plan/plan_test.go` (extend)

- Verify `StepFirewallSet` appears after `StepComputeSet`, before `StepVolumeSet`
- Verify params contain the firewall rules
- Verify no `StepFirewallSet` when no firewall config

## Files changed (summary)

| File | Change |
|------|--------|
| `pkg/provider/compute.go` | `PortAllowList` type, `ReconcileFirewallRules` + `GetFirewallRules` on interface |
| `pkg/provider/allowlist.go` | **New.** `ResolveFirewallArgs`, `resolvePreset`, `fetchCloudflareIPs`, `mergeAllowLists`, `parseRawRules` |
| `pkg/provider/allowlist_test.go` | **New.** Parse tests |
| `pkg/provider/hetzner/firewall.go` | `defaultFirewallRules` → `baseFirewallRules` (no 80/443), implement interface methods |
| `pkg/provider/aws/firewall.go` | Same rename, implement interface methods |
| `pkg/provider/scaleway/firewall.go` | Same rename, implement interface methods |
| `pkg/core/firewall.go` | **New.** `FirewallSet`, `FirewallList` |
| `internal/core/firewall.go` | **New.** Cobra commands |
| `internal/core/root.go` | Register firewall command |
| `pkg/kube/caddy.go` | `IngressRoute.Proxy bool`, `:80` block generation for proxied routes |
| `pkg/kube/caddy_test.go` | Proxied caddyfile generation + roundtrip tests |
| `pkg/core/dns.go` | `DNSSetRequest.Proxy bool`, thread to `IngressRoute`, pass to Cloudflare `EnsureARecord` |
| `pkg/provider/cloudflare/dns.go` | `EnsureARecord` accepts `proxied` parameter |
| `internal/core/dns.go` | `--proxy` flag, hard error if dns provider != cloudflare |
| `internal/api/config/schema.go` | `Config.Firewall` field, `DomainConfig.Proxy` field |
| `internal/api/config/validate.go` | Domains-without-firewall + firewall×proxy coherence validation |
| `internal/api/plan/plan.go` | `StepFirewallSet` kind + `setFirewall` phase |
| `internal/api/handlers/executor.go` | `StepFirewallSet` dispatch |
| `internal/testutil/mock_provider.go` | Add `ReconcileFirewallRules` + `GetFirewallRules` to mock |
| Tests (multiple files) | New + extended |

## Example updates

All examples use Cloudflare as DNS provider. Every deploy script and config YAML
that has domains needs a `firewall set` step.

**Hetzner examples use `cloudflare` preset + `--proxy`** — showcases the full
Cloudflare-first security posture (CF terminates TLS, firewall locked to CF IPs).

**AWS and Scaleway examples use `default` preset** — vanilla mode, Caddy does TLS,
firewall open to all on 80/443. Still uses Cloudflare as DNS provider, just not proxied.

### Direct CLI (`examples/core/`)

**`examples/core/hetzner/deploy`** — add after `instance set`, before build:

```bash
bin/core firewall set cloudflare
```

Change dns line:

```bash
bin/core dns set web hz.nvoi.to --proxy
```

**`examples/core/aws/deploy`** — add after `instance set`:

```bash
bin/core firewall set default
```

dns line unchanged (no `--proxy`).

**`examples/core/scaleway/deploy`** — add after `instance set`:

```bash
bin/core firewall set default
```

dns line unchanged (no `--proxy`).

**Destroy scripts** — no changes. `firewall set` is idempotent, and the firewall
is deleted when `instance delete` removes the last server (existing behavior).

### Cloud CLI (`examples/cloud/`)

**`examples/cloud/hetzner/config.yaml`** — add:

```yaml
firewall: cloudflare

domains:
  web:
    domains: [hz-cloud.nvoi.to]
    proxy: true
```

**`examples/cloud/aws/config.yaml`** — add:

```yaml
firewall: default
```

domains line unchanged.

**`examples/cloud/scaleway/config.yaml`** — add:

```yaml
firewall: default
```

domains line unchanged.

### Summary

| Example | Firewall preset | `--proxy` | TLS by | Firewall 80/443 |
|---------|----------------|-----------|--------|-----------------|
| `core/hetzner` | `cloudflare` | yes | Cloudflare edge | CF IPs only |
| `core/aws` | `default` | no | Caddy (ACME) | `0.0.0.0/0` |
| `core/scaleway` | `default` | no | Caddy (ACME) | `0.0.0.0/0` |
| `cloud/hetzner` | `cloudflare` | yes | Cloudflare edge | CF IPs only |
| `cloud/aws` | `default` | no | Caddy (ACME) | `0.0.0.0/0` |
| `cloud/scaleway` | `default` | no | Caddy (ACME) | `0.0.0.0/0` |

## Execution order

1. `pkg/provider/allowlist.go` + tests — standalone
2. `pkg/provider/compute.go` — add type + interface methods
3. Rename `defaultFirewallRules` → `baseFirewallRules` in all three providers (remove 80/443)
4. Implement `ReconcileFirewallRules` + `GetFirewallRules` in all three providers
5. `pkg/core/firewall.go` — business logic
6. `pkg/kube/caddy.go` — `IngressRoute.Proxy`, `:80` block generation
7. `pkg/core/dns.go` — `--proxy` flag, thread to Caddy + Cloudflare
8. `internal/core/firewall.go` + `internal/core/dns.go` — CLI commands + flag
9. `internal/api/` — schema, validation, plan, executor
10. Update mock + all tests
11. Update all 6 examples (`examples/core/` + `examples/cloud/`) + `examples/README.md`
