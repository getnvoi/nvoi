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

// ResourceGroup is a named table of resources returned by ListResources.
// Each provider returns its own groups — the provider knows what it created.
type ResourceGroup struct {
	Name    string     `json:"name"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
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
