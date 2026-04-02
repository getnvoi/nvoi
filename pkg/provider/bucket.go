package provider

import "context"

// BucketProvider abstracts object storage operations.
// Implementations: cloudflare (R2), aws (future), hetzner (future).
type BucketProvider interface {
	ValidateCredentials(ctx context.Context) error
	EnsureBucket(ctx context.Context, name string) error
	EmptyBucket(ctx context.Context, name string) error
	DeleteBucket(ctx context.Context, name string) error
	SetCORS(ctx context.Context, name string, origins, methods []string) error
	ClearCORS(ctx context.Context, name string) error
	SetLifecycle(ctx context.Context, name string, expireDays int) error
	Credentials(ctx context.Context) (BucketCredentials, error)
}

// BucketCredentials holds S3-compatible access details for injection into services.
type BucketCredentials struct {
	Endpoint        string // e.g. https://acct.r2.cloudflarestorage.com
	AccessKeyID     string
	SecretAccessKey string
	Region          string // "auto", "eu-central-1", etc.
}
