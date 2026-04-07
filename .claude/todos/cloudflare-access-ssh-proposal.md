# Cloudflare Access SSH — Implementation Proposal

Opt-in identity-based SSH access for SaaS mode. No cloudflared on servers. No static IPs.
No IP allowlisting for SSH. Port 22 open but cert-gated — no valid CF-signed certificate,
no access.

Separate from the `allowed-ips` proposal. Can be built on top of it or independently.
`allowed-ips` handles firewall-level port restrictions (80/443 to CF IPs, etc.).
This proposal handles SSH auth.

## How it works

### One-time setup (nvoi infrastructure)

1. Cloudflare Zero Trust: create Access Application for `*.ssh.nvoi.to` (type SSH)
2. Create Service Token → `CF_ACCESS_CLIENT_ID` + `CF_ACCESS_CLIENT_SECRET`
3. Access Policy: allow service token
4. Fetch CA public key:

```bash
curl https://api.cloudflare.com/client/v4/accounts/{account_id}/access/apps/{app_id}/ca \
  -H "Authorization: Bearer <token>"
```

Response: `{"public_key": "ecdsa-sha2-nistp256 AAAA..."}` — stable, doesn't rotate
unless explicitly regenerated.

5. Store CA public key in nvoi config (env var, DB, or hardcoded constant).

### Per-server (`instance set` with access mode)

**Cloud-init — two additions to `infra.RenderCloudInit`:**

```yaml
write_files:
  - path: /etc/ssh/cf_ca.pub
    content: "ecdsa-sha2-nistp256 AAAA..."   # CF CA public key from step 4

runcmd:
  - echo "TrustedUserCAKeys /etc/ssh/cf_ca.pub" >> /etc/ssh/sshd_config
  - systemctl restart sshd
```

One file written. One config line appended. sshd restart. No binary installed.
This runs alongside the existing cloud-init that sets SSH public key + hostname.

**Firewall — locked-down set:**

```
22   → 0.0.0.0/0 (open, but cert-gated by sshd — see below)
80   → Cloudflare IPs (~15 stable CIDRs)
443  → Cloudflare IPs (or closed — CF talks to origin on 80)
6443 → private network only
...rest unchanged
```

Port 22 is open at the firewall level. Auth is handled by sshd — only certificates
signed by CF's CA are accepted. Password auth disabled (already the case).
Static key auth can be kept as fallback for direct CLI mode, or removed.

### Per-SSH operation (deploy, service set, dns set, etc.)

```
1. Authenticate with CF Access:
   POST https://<team>.cloudflareaccess.com/cdn-cgi/access/certs/sign
   Headers:
     CF-Access-Client-Id: <service-token-id>
     CF-Access-Client-Secret: <service-token-secret>
   Body:
     {"public_key": "<nvoi's ed25519 public key>"}

   → CF signs nvoi's public key with CF's CA private key
   → Returns short-lived SSH certificate (~2 min validity)

2. SSH to server:22 using the certificate:
   golang.org/x/crypto/ssh with cert as auth method
   → sshd validates cert against /etc/ssh/cf_ca.pub
   → match → session established

3. Session persists beyond cert expiry (SSH sessions stay alive once established)
```

## Connection flow

```
nvoi API                    Cloudflare                     Customer server
    |                           |                              |
    |-- service token --------->|                              |
    |   + nvoi's public key     |                              |
    |<-- signed SSH cert -------|                              |
    |                           |                              |
    |-- SSH with cert (direct TCP, port 22) ------------------>|
    |                           |              sshd validates   |
    |                           |              cert against     |
    |                           |              cf_ca.pub        |
    |<----- session established --------------------------------|
```

Direct TCP connection to the server. Not proxied through Cloudflare.
Cloudflare's only role is signing the certificate — the SSH traffic goes
directly from nvoi to the server.

## What changes in the code

### New: `pkg/provider/cloudflare/access.go`

CF Access API client. Two operations:

```go
// AccessClient handles Cloudflare Access certificate signing.
type AccessClient struct {
    team         string // CF Zero Trust team name
    clientID     string // service token ID
    clientSecret string // service token secret
    caPubKey     string // CA public key (fetched once, stable)
}

// SignSSHKey sends nvoi's public key to CF Access and returns a
// short-lived SSH certificate signed by CF's CA.
func (c *AccessClient) SignSSHKey(ctx context.Context, pubKey string) ([]byte, error) {
    // POST https://<team>.cloudflareaccess.com/cdn-cgi/access/certs/sign
    // Headers: CF-Access-Client-Id, CF-Access-Client-Secret
    // Body: {"public_key": pubKey}
    // Returns: signed certificate bytes
}

// CAPubKey returns the CA public key for cloud-init injection.
func (c *AccessClient) CAPubKey() string {
    return c.caPubKey
}
```

Uses `utils.HTTPClient`. Same patterns as existing Cloudflare DNS/R2 code.

### Modified: `infra.RenderCloudInit`

**File:** `pkg/infra/cloudinit.go`

Currently accepts `sshPublicKey` and `hostname`. Add optional `caPubKey`:

```go
func RenderCloudInit(sshPublicKey, hostname, caPubKey string) (string, error)
```

When `caPubKey` is non-empty, the cloud-init template includes:

```yaml
write_files:
  - path: /etc/ssh/cf_ca.pub
    content: "{caPubKey}"
    permissions: "0644"

runcmd:
  - |
    if ! grep -q TrustedUserCAKeys /etc/ssh/sshd_config; then
      echo "TrustedUserCAKeys /etc/ssh/cf_ca.pub" >> /etc/ssh/sshd_config
      systemctl restart sshd
    fi
```

Idempotent — the grep guard prevents duplicate lines on re-provision.
When `caPubKey` is empty, cloud-init is unchanged (backward compatible).

### Modified: `infra.ConnectSSH`

**File:** `pkg/infra/ssh.go`

Currently connects with a static private key:

```go
func ConnectSSH(ctx context.Context, addr, user string, privKey []byte) (utils.SSHClient, error)
```

Add a variant (or parameter) for cert-based auth:

```go
func ConnectSSHWithCert(ctx context.Context, addr, user string, privKey, cert []byte) (utils.SSHClient, error)
```

The `cert` is the short-lived certificate from `AccessClient.SignSSHKey`.
Internally, `golang.org/x/crypto/ssh` supports certificate auth natively:

```go
signer, _ := ssh.ParsePrivateKey(privKey)
certParsed, _ := ssh.ParsePublicKey(cert)
certSigner, _ := ssh.NewCertSigner(certParsed.(*ssh.Certificate), signer)

config := &ssh.ClientConfig{
    User: user,
    Auth: []ssh.AuthMethod{ssh.PublicKeys(certSigner)},
}
```

Same `SSHClient` interface returned. Everything downstream is identical.

### Modified: `Cluster.SSH()`

**File:** `pkg/core/cluster.go`

`Cluster.SSH()` resolves master → connects via SSH. In access mode, it:

1. Calls `AccessClient.SignSSHKey(ctx, pubKey)` → gets short-lived cert
2. Calls `infra.ConnectSSHWithCert(ctx, addr, user, privKey, cert)` → returns SSHClient
3. Rest of the flow is identical

The `Cluster` struct gets an optional `Access *AccessClient` field.
When nil → direct SSH (current behavior). When set → cert-based auth.

```go
type Cluster struct {
    AppName, Env, Provider string
    Credentials            map[string]string
    SSHKey                 []byte
    Output                 Output
    SSHFunc                func(ctx, addr) (SSHClient, error)
    Access                 *cloudflare.AccessClient  // NEW — nil = direct SSH
}
```

### Modified: `ComputeSet`

**File:** `pkg/core/compute.go`

When `req.Cluster.Access` is set:

1. Get CA public key from `Access.CAPubKey()`
2. Pass to `infra.RenderCloudInit(pubKey, serverName, caPubKey)`
3. First SSH connection (waiting for server boot) uses the cert flow

```go
// In ComputeSet, before RenderCloudInit:
var caPubKey string
if req.Cluster.Access != nil {
    caPubKey = req.Cluster.Access.CAPubKey()
}

userData, err := infra.RenderCloudInit(strings.TrimSpace(pubKey), serverName, caPubKey)
```

### Modified: firewall rules (when access mode is on)

When access mode is active, the default firewall rules for 80/443 switch to
Cloudflare IP ranges. Port 22 stays open to `0.0.0.0/0` (cert-gated, not
IP-gated).

This integrates with the `PortAllowList` from the allowed-ips proposal:

```go
// In SaaS mode with CF Access:
allowedIPs := provider.PortAllowList{
    "80":  cloudflareIPv4Ranges,  // ~15 stable CIDRs
    "443": cloudflareIPv4Ranges,
    // 22 absent → defaults to 0.0.0.0/0 (open, cert-gated)
}
```

Cloudflare IPv4 ranges (from https://www.cloudflare.com/ips/):

```go
var cloudflareIPv4Ranges = []string{
    "173.245.48.0/20",
    "103.21.244.0/22",
    "103.22.200.0/22",
    "103.31.4.0/22",
    "141.101.64.0/18",
    "108.162.192.0/18",
    "190.93.240.0/20",
    "188.114.96.0/20",
    "197.234.240.0/22",
    "198.41.128.0/17",
    "162.158.0.0/15",
    "104.16.0.0/13",
    "104.24.0.0/14",
    "172.64.0.0/13",
    "131.0.72.0/22",
}
```

These change rarely. Hardcode them. Add known-limitations note about staleness.
Can add a refresh mechanism later (GET https://api.cloudflare.com/client/v4/ips).

## Credential schema

New credential fields for CF Access. Added to the existing Cloudflare credential schema
in `pkg/provider/cloudflare/register.go`:

```go
// Existing
CF_API_KEY     // Cloudflare API key (DNS, R2)
CF_ACCOUNT_ID  // Cloudflare account ID
CF_ZONE_ID     // DNS zone ID

// New (only needed when access mode is on)
CF_ACCESS_TEAM      // Zero Trust team name (e.g. "mycompany")
CF_ACCESS_CLIENT_ID // Service token ID
CF_ACCESS_CLIENT_SECRET // Service token secret
CF_ACCESS_APP_ID    // Access application ID (for CA key fetch)
```

In SaaS mode, these come from the DB (encrypted, like all credentials).
In direct CLI mode, from `.env` (opt-in).

## CLI UX

### Direct CLI (opt-in)

```bash
# .env
CF_ACCESS_TEAM=mycompany
CF_ACCESS_CLIENT_ID=xxx
CF_ACCESS_CLIENT_SECRET=xxx
CF_ACCESS_APP_ID=xxx

# Deploy with CF Access SSH
nvoi instance set master --compute-type cx23 --compute-region fsn1 --ssh-access cloudflare
```

`--ssh-access cloudflare` activates the flow. Without it → direct SSH (current behavior).

### SaaS config YAML

```yaml
servers:
  master:
    type: cx23
    region: fsn1

ssh_access: cloudflare   # workspace-level or repo-level setting
```

Or as a workspace-level setting in the DB, not per-config.

## Interaction with allowed-ips proposal

The two proposals are complementary:

| Concern | allowed-ips | CF Access SSH |
|---|---|---|
| Port 22 | IP-restricted at firewall | Cert-gated at sshd |
| Port 80/443 | IP-restricted at firewall | CF IPs at firewall |
| Requires CF | No | Yes |
| Static IPs | Needs known source IPs | No |

They can be combined:

```
Port 22: open at firewall (cert-gated by sshd via CF Access)
Port 80: Cloudflare IPs only (from allowed-ips PortAllowList)
Port 443: Cloudflare IPs only (from allowed-ips PortAllowList)
```

Or used independently. `allowed-ips` works without CF Access (IP-based).
CF Access works without `allowed-ips` (identity-based for SSH, CF proxy for HTTP).

## Security properties

| Property | Direct SSH (current) | CF Access SSH |
|---|---|---|
| Auth method | Static ed25519 key | Short-lived cert (~2 min) |
| Key rotation | Manual | Automatic (new cert per operation) |
| Credential compromise | Permanent access until key revoked | Access revocable instantly in CF dashboard |
| IP dependency | Need to know/trust source IP | None — identity-based from any IP |
| Audit trail | Server auth log only | CF Access audit log + server auth log |
| Port 22 exposure | Open, key-gated | Open, cert-gated (stronger — certs expire) |

## Files changed

| File | Change |
|------|--------|
| `pkg/provider/cloudflare/access.go` | **New.** AccessClient, SignSSHKey, CAPubKey |
| `pkg/provider/cloudflare/access_test.go` | **New.** Tests with httptest server |
| `pkg/provider/cloudflare/register.go` | Add CF_ACCESS_* credential fields to schema |
| `pkg/infra/cloudinit.go` | `RenderCloudInit` accepts optional caPubKey |
| `pkg/infra/cloudinit_test.go` | Test cloud-init with/without CA key |
| `pkg/infra/ssh.go` | Add `ConnectSSHWithCert` for cert-based auth |
| `pkg/core/cluster.go` | `Cluster.Access` field, cert-aware `SSH()` |
| `pkg/core/compute.go` | Pass CA pubkey to cloud-init when access mode on |
| `internal/core/instance.go` | `--ssh-access` flag |
| `internal/core/resolve.go` | Resolve CF Access credentials from env |
| `internal/api/handlers/executor.go` | Build AccessClient when workspace has CF Access |
| `internal/api/config/schema.go` | `ssh_access` field |

No changes to:
- `SSHClient` interface (same contract)
- `kube/` (uses SSH, doesn't care how it's authed)
- `provider/hetzner|aws|scaleway` (firewall changes come from allowed-ips, not here)
- `internal/render/` (output is identical)
- Any existing tests (backward compatible, access mode is opt-in)

## Execution order

1. `pkg/provider/cloudflare/access.go` + tests — standalone, calls CF API
2. `pkg/infra/cloudinit.go` — add caPubKey parameter + tests
3. `pkg/infra/ssh.go` — add `ConnectSSHWithCert` + tests
4. `pkg/core/cluster.go` — Access field, cert-aware SSH flow
5. `pkg/core/compute.go` — pass CA key to cloud-init
6. `internal/core/` — flag + credential resolution
7. `internal/api/` — schema + executor integration

## Open questions

1. **Static key fallback?** When CF Access is on, should the static ed25519 key
   still be accepted by sshd? Useful for emergency access if CF is down.
   sshd accepts multiple auth methods — both `TrustedUserCAKeys` and
   `AuthorizedKeysFile` can coexist. Recommend: keep both, document that
   static key is the emergency backdoor.

2. **Cert expiry window.** CF certs are ~2 minutes. A deploy takes longer.
   SSH sessions persist beyond cert expiry (the cert is only checked at handshake).
   But if a deploy needs multiple sequential SSH connections (e.g. master + worker),
   nvoi should fetch a fresh cert per connection, not reuse a stale one.
   `Cluster.SSH()` already creates a new connection per call — just fetch
   a new cert each time.

3. **CF API rate limits.** One cert signing request per SSH connection.
   A typical deploy has ~5-10 SSH operations. CF Access API limits are
   generous (thousands/min). Not a concern at any reasonable scale.

4. **DNS for SSH hostnames.** The proposal uses the server's IP directly for SSH
   (same as today). The `*.ssh.nvoi.to` hostnames from the one-time setup are
   for the CF Access application definition, not for actual SSH connections.
   nvoi SSHes to the IP, sshd validates the cert. No DNS resolution needed
   for the SSH connection itself.
