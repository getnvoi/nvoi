package provider

import "context"

// Builder abstracts image building from source repos.
// Implementations: daytona, local (future — docker buildx + SSH tunnel push).
type Builder interface {
	// Build clones the repo, builds the image, pushes to the target registry.
	// Returns the full image ref (e.g. "localhost:5000/myapp:20260331-143000").
	Build(ctx context.Context, req BuildRequest) (*BuildResult, error)
}

type BuildRequest struct {
	Repo         string // git ref: "myorg/myapp", "https://github.com/...", "git@github.com:..."
	Branch       string // git branch (default "main")
	RegistryAddr string // push target (e.g. "10.0.0.2:5000")
	Tag          string // image tag (e.g. "20260331-143000")
}

type BuildResult struct {
	ImageRef string // full image ref pushed to registry
}
