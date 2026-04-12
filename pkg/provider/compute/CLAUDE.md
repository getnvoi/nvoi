# CLAUDE.md — pkg/provider/compute

Compute providers manage servers, firewalls, networks, and volumes. All three implementations (Hetzner, AWS, Scaleway) must follow the same behavioral contract regardless of API differences.

## DeleteServer contract

`DeleteServer` is a complete cleanup of the server. The caller must not need to know about provider-specific cascading behavior.

**Required steps, in order:**

1. Look up server by name. If absent, return `nil` (idempotent — already deleted, nothing to do).
2. **Detach firewall** from the server. Execute and **verify detachment completed** before proceeding. Provider-specific:
   - Hetzner: `POST /firewalls/{id}/actions/remove_from_resources` → poll action until complete
   - AWS: security groups are released on instance termination, but explicit removal from SG before terminate is safer — verify SG no longer lists the instance
   - Scaleway: security group association drops on server delete, but verify the server is no longer listed in the SG
3. **Detach all volumes** from the server. Execute and **verify each detachment completed** before proceeding. Provider-specific:
   - Hetzner: `POST /volumes/{id}/actions/detach` → poll action until complete
   - AWS: `DetachVolume` → poll until volume state = `available`
   - Scaleway: `PATCH /servers/{id}` to clear volume map → verify volumes no longer attached
4. **Delete the server.**
5. **Wait for server gone.** Poll until the provider confirms the server no longer exists.
   - Hetzner: poll `GET /servers/{id}` until 404
   - AWS: poll `DescribeInstances` until state = `terminated`
   - Scaleway: poll `GET /servers/{id}` until 404

**DeleteServer does NOT:**
- Delete the firewall. That's `DeleteFirewall`'s job, called separately.
- Delete the network. That's `DeleteNetwork`'s job, called separately.
- Delete volumes. Volumes are user data. Detach only. Deletion requires explicit `--delete-volumes`.

## DeleteFirewall contract

Deletes the firewall resource by name. Called after all servers have been deleted and detached.

- Must succeed if the firewall exists and has no attached servers.
- If absent, return `nil` (idempotent).
- Must NOT fail because a server is still attached — `DeleteServer` guarantees detachment before this is called.

## DeleteNetwork contract

Deletes the network resource by name. Called after all servers have been deleted.

- Must succeed if the network exists and has no attached resources.
- If absent, return `nil` (idempotent).
- AWS-specific: must clean up IGWs, subnets, route tables before VPC delete.

## Volume lifecycle

Volumes are user data. The rules:

1. **`DetachVolume`** — detaches a volume from its server. Volume still exists, data preserved. Safe, reversible.
2. **`DeleteVolume`** — destroys the volume and its data. Only called when user passes `--delete-volumes`. Must detach first if still attached.
3. **`DeleteServer`** — detaches volumes but never deletes them. After server deletion, volumes exist detached, ready to be reattached on next deploy.

## Teardown order

```
1. DNS records (external, at DNS provider)
2. Storage buckets (only with --delete-storage)
3. Package resources (database backup buckets, etc.)
4. Volumes (only with --delete-volumes)
5. Servers — workers first, then master
   └── Each server: detach firewall → detach volumes → delete → wait gone
6. Firewall (always deleted — safe because servers are gone)
7. Network (always deleted — safe because servers are gone)
```

## Provider-specific notes

### Hetzner
- `detachFirewall()` helper exists but was never called from `DeleteServer` — must be wired in.
- Server delete does NOT wait — must add polling until 404.
- Volumes auto-detach on server delete, but explicit detach before delete ensures clean ordering.
- Firewall delete fails if still applied to a server (proven in production). Detach-first is required.

### AWS
- Instance termination is async. `DeleteServer` already polls for `terminated` state.
- Security groups (firewalls) cannot be deleted while attached to any instance, even terminated ones in cooldown. `DeleteServer` waiting for terminated state is necessary but may not be sufficient — SG delete may need retry.
- Volumes survive instance termination. `DeleteServer` must explicitly detach before terminating.
- Network cleanup requires cascading: IGWs, subnets, route tables before VPC delete.

### Scaleway
- Server terminate is async. `DeleteServer` already polls until gone.
- Security groups can be deleted regardless of attachment state.
- Volumes auto-detach on server delete.
- Private network delete may fail if NICs are still attached — server deletion clears this.

## Error handling

- `DeleteServer` detach steps must succeed before proceeding to server deletion. If firewall detach fails, return the error — don't delete a server with resources still attached.
- `DeleteFirewall` and `DeleteNetwork` errors in teardown are currently swallowed (`_ =`). This should log warnings but not fail the teardown.
- All delete operations are idempotent: calling twice is safe. If the resource is already gone, return `nil`, not an error. This applies to `DeleteServer`, `DeleteFirewall`, `DeleteNetwork`, `DeleteVolume`, and `DetachVolume`.
