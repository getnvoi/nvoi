package provider

import "context"

// SecretsProvider abstracts external secrets management.
// Implementations: doppler, awssm (AWS Secrets Manager), infisical.
type SecretsProvider interface {
	ValidateCredentials(ctx context.Context) error
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context) ([]string, error)
}
