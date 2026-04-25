package provider

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

// Scope is the binary classification surfaced in the Scope column of
// `nvoi resources`. Tells the operator whether each row is in scope
// of THIS `./nvoi.yaml` right now:
//
//   - ScopeOwned    — name appears in cfg's expected-set for its kind.
//   - ScopeExternal — anything else (manual, another project, prior
//     deploy with cfg now changed, ambiguous nvoi-shaped names we
//     can't actually verify).
//
// We deliberately do NOT try to distinguish "stale orphan" from
// "different nvoi project" from "manual" — they're all just
// "external to current cfg". The structural `nvoi-{app}-{env}-...`
// naming pattern looks like a provenance signal but isn't one (any
// operator can name a manual bucket that way). Cfg match is the only
// answer we can give honestly.
type Scope string

const (
	ScopeOwned    Scope = "owned"
	ScopeExternal Scope = "external"
)

// OwnershipContext is the cfg-derived expected-name set the classifier
// compares each row against. nil context (no cfg loaded) → every row
// classifies as ScopeExternal.
type OwnershipContext struct {
	ExpectedServers   map[string]bool
	ExpectedVolumes   map[string]bool
	ExpectedFirewalls map[string]bool
	ExpectedNetworks  map[string]bool
	ExpectedDNS       map[string]bool
	ExpectedBuckets   map[string]bool
	ExpectedTunnels   map[string]bool
}

// ResourceGroup is a named table of resources returned by ListResources.
// Each provider returns its own groups; classification (`Scope[i]`) is
// added at the consumer (pkg/core.Classify) — providers stay
// oblivious to the OwnershipContext concept.
type ResourceGroup struct {
	Name    string     `json:"name"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	Scope   []Scope    `json:"scope,omitempty"`
}

// ClassifyScope is the only classifier — name in expected-set →
// owned, otherwise → external. No structural inference, no provenance
// claims.
func ClassifyScope(name string, expected map[string]bool) Scope {
	if expected[name] {
		return ScopeOwned
	}
	return ScopeExternal
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
