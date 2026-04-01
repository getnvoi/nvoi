package provider

import (
	"context"
	"io"
)

// BuildProvider builds container images and pushes them to the cluster registry.
// Implementations: local (docker buildx + SSH tunnel), daytona (remote sandbox), hetzner (one-off server).
type BuildProvider interface {
	Build(ctx context.Context, req BuildRequest) (*BuildResult, error)
}

// BuildRequest is everything the builder needs.
// The caller prepares this fully resolved — the builder doesn't read env vars or config files.
type BuildRequest struct {
	ServiceName string    // service being built (used in image tag)
	Source      string    // local path (./path) or remote repo (org/repo, https://..., git@...)
	Branch      string    // git branch (remote only, default "main")
	Platform    string    // "linux/amd64" or "linux/arm64" (auto-detected if empty)
	RegistrySSH SSHAccess // SSH tunnel to reach the private registry
	Stdout      io.Writer
	Stderr      io.Writer
}

// SSHAccess provides what the builder needs to tunnel to the cluster registry.
type SSHAccess struct {
	MasterIP        string // public IP of master server
	MasterPrivateIP string // private IP where registry listens
	PrivKey         []byte // SSH private key
}

// BuildResult is what the builder returns.
type BuildResult struct {
	ImageRef string // full registry reference: "10.0.1.1:5000/web:20260401-120000"
}
