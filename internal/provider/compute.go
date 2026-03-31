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

	// Volume
	EnsureVolume(ctx context.Context, req CreateVolumeRequest) (*Volume, error)
	DetachVolume(ctx context.Context, name string, labels map[string]string) error
	ListVolumes(ctx context.Context, labels map[string]string) ([]*Volume, error)
}

type Server struct {
	ID, Name, Status, IPv4, IPv6, PrivateIP string
}

type Volume struct {
	ID, Name   string
	Size       int
	ServerID   string
	DevicePath string
}

type CreateServerRequest struct {
	Name, ServerType, Image, Location, UserData string
	Labels                                      map[string]string
}

type DeleteServerRequest struct {
	Name   string
	Labels map[string]string
}

type CreateVolumeRequest struct {
	Name, Location, ServerID string
	Size                     int
	Labels                   map[string]string
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
