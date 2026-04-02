// Package hetzner implements provider.ComputeProvider for Hetzner Cloud.
//
// Ported from nvoi-platform internal/platform/compute/hetzner/.
// Files to carry over:
//   - client.go   (New, ValidateCredentials, ArchForType, IsNotFound)
//   - server.go   (GetServerByName, EnsureServer, DeleteServer, ListServers)
//   - firewall.go (GetFirewallByName, CreateFirewall, SetFirewallRules, DeleteFirewall, DetachFirewall, ListFirewalls)
//   - network.go  (GetNetworkByName, CreateNetwork, DeleteNetwork, ListNetworks)
//   - volume.go   (GetVolumeByName, CreateVolume, AttachVolume, DetachVolume, GetVolume, DeleteVolume, ListVolumes)
package hetzner

// TODO Phase 1: Port from nvoi-platform/internal/platform/compute/hetzner/
// All 5 files (~825 lines) carry over with minimal changes.
// The only change: import path (provider interface moves to internal/provider/).
