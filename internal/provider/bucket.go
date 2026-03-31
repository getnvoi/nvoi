package provider

import "context"

// BucketProvider abstracts object storage operations.
// Implementations: cloudflare (R2), aws (future), hetzner (future).
type BucketProvider interface {
	ValidateCredentials(ctx context.Context) error
	EnsureBucket(ctx context.Context, name string) error
	SetCORS(ctx context.Context, name string) error
	ClearCORS(ctx context.Context, name string) error
	SetLifecycle(ctx context.Context, name string, days int) error
	Credentials() (*BucketCredentials, error)
}

type BucketCredentials struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
}
