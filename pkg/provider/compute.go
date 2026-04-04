package provider

import "context"

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

// ComputeProvider abstracts cloud compute operations.
type ComputeProvider interface {
	ValidateCredentials(ctx context.Context) error
	ArchForType(instanceType string) string

	// Server — firewall + network resolved internally
	EnsureServer(ctx context.Context, req CreateServerRequest) (*Server, error)
	DeleteServer(ctx context.Context, req DeleteServerRequest) error
	ListServers(ctx context.Context, labels map[string]string) ([]*Server, error)

	// Resources — unfiltered, everything under the account
	ListAllFirewalls(ctx context.Context) ([]*Firewall, error)
	ListAllNetworks(ctx context.Context) ([]*Network, error)

	// ListResources returns all provider-managed resources as display groups.
	// Each provider lists everything it created — no leftovers go unnoticed.
	ListResources(ctx context.Context) ([]ResourceGroup, error)

	// Volume
	EnsureVolume(ctx context.Context, req CreateVolumeRequest) (*Volume, error)
	DetachVolume(ctx context.Context, name string) error
	DeleteVolume(ctx context.Context, name string) error
	ListVolumes(ctx context.Context, labels map[string]string) ([]*Volume, error)

	// GetPrivateIP resolves the private IP of a server from the provider API.
	// Always queries live state — never trust Server.PrivateIP from ListServers.
	// Hetzner: re-fetches server. AWS: re-fetches instance. Scaleway: IPAM lookup.
	GetPrivateIP(ctx context.Context, serverID string) (string, error)

	// ResolveDevicePath returns the OS block device path for an attached volume.
	// Provider-specific: Hetzner returns LinuxDevice from API, AWS computes the NVMe symlink.
	ResolveDevicePath(vol *Volume) string
}

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
	Labels                                      map[string]string
}

type DeleteServerRequest struct {
	Name, FirewallName, NetworkName string
	Labels                         map[string]string
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
