package infra

// TODO Phase 4: Port from nvoi-platform/internal/infra/destroy.go (~136 lines)
//
// Tears down infrastructure by hitting real APIs in reverse order:
//   1. kubectl delete all nvoi resources (SSH → kubectl on remote)
//   2. Detach volumes (provider API — data preserved, volumes NOT deleted)
//   3. Delete servers (provider API)
//   4. Delete firewall + network (provider API)
//   5. Delete DNS records (DNS API)
//
// Every step hits real infrastructure. Errors collected, not short-circuited.
// 404s treated as success (already gone). Best-effort teardown.
