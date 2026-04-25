package provider

import "strings"

// Shared resource types that InfraProvider implementations expose AND
// the higher layer (config, reconcile, render) reads. Lifted out of the
// (now-deleted) pkg/provider/compute.go which housed both the
// ComputeProvider interface and these types. The interface is gone in
// refactor #47 (C10); the types stay because each concrete provider
// still surfaces servers / volumes / firewalls / networks via its own
// public methods (EnsureServer, ListVolumes, etc.) called from the
// provider-internal Bootstrap / Teardown helpers.

// ServerStatus represents the state of a compute server.
type ServerStatus string

const (
	ServerRunning      ServerStatus = "running"
	ServerInitializing ServerStatus = "initializing"
	ServerStarting     ServerStatus = "starting"
	ServerStopping     ServerStatus = "stopping"
	ServerOff          ServerStatus = "off"
	ServerDeleting     ServerStatus = "deleting"
	ServerMigrating    ServerStatus = "migrating"
	ServerRebuilding   ServerStatus = "rebuilding"
	ServerUnknown      ServerStatus = "unknown"
)

type Firewall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Network struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Server struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    ServerStatus `json:"status"`
	IPv4      string       `json:"ipv4"`
	IPv6      string       `json:"ipv6,omitempty"`
	PrivateIP string       `json:"private_ip"`
	DiskGB    int          `json:"disk_gb,omitempty"`
}

type Volume struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Size       int    `json:"size"`
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
	DevicePath string `json:"device_path"`
	Location   string `json:"location"`
}

type CreateServerRequest struct {
	Name, ServerType, Image, Location, UserData string
	FirewallName, NetworkName                   string
	DiskGB                                      int // root disk size; 0 = provider default
	Labels                                      map[string]string
}

type DeleteServerRequest struct {
	Name   string
	Labels map[string]string
}

// CreateVolumeRequest — provider resolves server name → ID internally.
type CreateVolumeRequest struct {
	Name, ServerName string
	Size             int
	Labels           map[string]string
}

// Ownership is the four-state classification surfaced in the Owned
// column of `nvoi resources`. Tells the operator what each row's
// relationship to THIS `./nvoi.yaml` is:
//
//   - OwnershipNone  — not nvoi-shaped (manual / external / other tool).
//   - OwnershipOther — nvoi-shaped, different app+env (another project).
//   - OwnershipStale — nvoi-shaped, this app+env, no longer in cfg.
//   - OwnershipLive  — nvoi-shaped, this app+env, in cfg.
//
// Pure structural — every nvoi-created resource is named
// `nvoi-{app}-{env}-{...}` (or exactly `nvoi-{app}-{env}` for
// tunnels), so the AppEnv segment is parseable from the name alone.
// Provider package emits rows with no ownership data; classification
// happens in the consumer (pkg/core.Classify) so the per-provider
// surface stays oblivious.
type Ownership string

const (
	OwnershipNone  Ownership = "no"
	OwnershipOther Ownership = "other"
	OwnershipStale Ownership = "stale"
	OwnershipLive  Ownership = "live"
)

// OwnershipContext is the cfg-derived input the classifier compares
// each row against. AppEnv is `nvoi-{app}-{env}` (Names.Base()).
// Expected* sets list the full deterministic names cfg currently asks
// for, per kind. Empty AppEnv → providers / classifier treat anything
// nvoi-shaped as OwnershipOther.
type OwnershipContext struct {
	AppEnv string

	ExpectedServers   map[string]bool
	ExpectedVolumes   map[string]bool
	ExpectedFirewalls map[string]bool
	ExpectedNetworks  map[string]bool
	ExpectedDNS       map[string]bool
	ExpectedBuckets   map[string]bool
	ExpectedTunnels   map[string]bool
}

// ResourceGroup is a named table of resources returned by ListResources.
// Each provider returns its own groups; classification (`Ownership[i]`)
// is added at the consumer (pkg/core.Classify) — providers stay
// oblivious to the OwnershipContext concept.
type ResourceGroup struct {
	Name      string      `json:"name"`
	Columns   []string    `json:"columns"`
	Rows      [][]string  `json:"rows"`
	Ownership []Ownership `json:"ownership,omitempty"`
}

// ClassifyByName applies the structural four-state rule against
// nvoi-named resources. Pure name match — no labels, no tags, no
// comments.
//
//   - Doesn't look nvoi-named → no
//   - Looks nvoi-named, ctx nil/empty AppEnv → other
//   - Name == ctx.AppEnv OR starts with ctx.AppEnv+"-": this app+env
//   - in expected → live
//   - else → stale
//   - Else (different nvoi project) → other
func ClassifyByName(name string, ctx *OwnershipContext, expected map[string]bool) Ownership {
	if !looksNvoiNamed(name) {
		return OwnershipNone
	}
	if ctx == nil || ctx.AppEnv == "" {
		return OwnershipOther
	}
	if name == ctx.AppEnv || strings.HasPrefix(name, ctx.AppEnv+"-") {
		if expected[name] {
			return OwnershipLive
		}
		return OwnershipStale
	}
	return OwnershipOther
}

// ClassifyByCfgMatch is the simpler classifier for resources whose
// names DON'T follow the nvoi naming pattern — Cloudflare DNS records
// being the canonical case (the name is a user-chosen FQDN). Without
// a structural signal we can only answer "in cfg" vs "not in cfg" →
// the other/stale distinction is unavailable.
func ClassifyByCfgMatch(name string, expected map[string]bool) Ownership {
	if expected[name] {
		return OwnershipLive
	}
	return OwnershipNone
}

// looksNvoiNamed reports whether `name` matches the nvoi naming
// pattern: prefix `nvoi-`, ≥3 non-empty dash-separated segments.
// Rejects 2-segment manual names like `nvoi-releases`.
func looksNvoiNamed(name string) bool {
	if !strings.HasPrefix(name, "nvoi-") {
		return false
	}
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
	}
	return true
}

type HTTPStatusError interface {
	HTTPStatus() int
}

func IsNotFound(err error) bool {
	if hse, ok := err.(HTTPStatusError); ok {
		return hse.HTTPStatus() == 404
	}
	return false
}
