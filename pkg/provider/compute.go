package provider

import "context"

// ComputeProvider abstracts cloud compute operations.
type ComputeProvider interface {
	ValidateCredentials(ctx context.Context) error
	ArchForType(instanceType string) string

	// Server — firewall + network resolved internally
	EnsureServer(ctx context.Context, req CreateServerRequest) (*Server, error)
	DeleteServer(ctx context.Context, req DeleteServerRequest) error
	ListServers(ctx context.Context, labels map[string]string) ([]*Server, error)

	// Resources — unfiltered, everything under the account
	ListAllServers(ctx context.Context) ([]*Server, error)
	ListAllFirewalls(ctx context.Context) ([]*Firewall, error)
	ListAllNetworks(ctx context.Context) ([]*Network, error)

	// Volume
	EnsureVolume(ctx context.Context, req CreateVolumeRequest) (*Volume, error)
	DetachVolume(ctx context.Context, name string, labels map[string]string) error
	DeleteVolume(ctx context.Context, name string, labels map[string]string) error // detach + delete cloud resource
	ListVolumes(ctx context.Context, labels map[string]string) ([]*Volume, error)
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
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	IPv4      string `json:"ipv4"`
	IPv6      string `json:"ipv6,omitempty"`
	PrivateIP string `json:"private_ip"`
}

type Volume struct {
	ID, Name   string
	Size       int
	ServerID   string
	ServerName string
	DevicePath string
	Location   string
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

type HTTPStatusError interface {
	HTTPStatus() int
}

func IsNotFound(err error) bool {
	if hse, ok := err.(HTTPStatusError); ok {
		return hse.HTTPStatus() == 404
	}
	return false
}
