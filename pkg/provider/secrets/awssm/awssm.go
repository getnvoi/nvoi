// Package awssm implements SecretsProvider using AWS Secrets Manager.
package awssm

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/awsbase"
)

// Client manages secrets via AWS Secrets Manager.
type Client struct {
	sm *secretsmanager.Client
}

func New(creds map[string]string) *Client {
	cfg, err := awsbase.LoadConfig(creds)
	if err != nil {
		// Factory is called after schema validation — if we can't load config
		// the credentials are malformed. ValidateCredentials will surface this.
		return &Client{}
	}
	return &Client{sm: secretsmanager.NewFromConfig(cfg)}
}

func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.sm == nil {
		return fmt.Errorf("awssm: failed to initialize — check credentials")
	}
	// List a single secret to verify credentials.
	_, err := c.sm.ListSecrets(ctx, &secretsmanager.ListSecretsInput{MaxResults: aws.Int32(1)})
	if err != nil {
		return fmt.Errorf("awssm: validate credentials: %w", err)
	}
	return nil
}

// Get returns the value for a secret key. Returns ("", nil) if the key
// does not exist — honoring the CredentialSource contract. Only real
// failures (auth, network) are returned as errors.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	if c.sm == nil {
		return "", fmt.Errorf("awssm: client not initialized")
	}
	out, err := c.sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(key),
	})
	if err != nil {
		var notFound *smtypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return "", nil
		}
		return "", fmt.Errorf("awssm: get %q: %w", key, err)
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("awssm: %q is a binary secret — only string secrets are supported", key)
	}
	return *out.SecretString, nil
}

func (c *Client) Set(ctx context.Context, key, value string) error {
	if c.sm == nil {
		return fmt.Errorf("awssm: client not initialized")
	}
	// Try update first, create if not found.
	_, err := c.sm.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(key),
		SecretString: aws.String(value),
	})
	if err != nil {
		// If the secret doesn't exist, create it.
		_, createErr := c.sm.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
			Name:         aws.String(key),
			SecretString: aws.String(value),
		})
		if createErr != nil {
			return fmt.Errorf("awssm: set %q: put failed: %w, create failed: %w", key, err, createErr)
		}
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	if c.sm == nil {
		return fmt.Errorf("awssm: client not initialized")
	}
	_, err := c.sm.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
		SecretId:                   aws.String(key),
		ForceDeleteWithoutRecovery: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("awssm: delete %q: %w", key, err)
	}
	return nil
}

func (c *Client) List(ctx context.Context) ([]string, error) {
	if c.sm == nil {
		return nil, fmt.Errorf("awssm: client not initialized")
	}
	var names []string
	paginator := secretsmanager.NewListSecretsPaginator(c.sm, &secretsmanager.ListSecretsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("awssm: list: %w", err)
		}
		for _, s := range page.SecretList {
			if s.Name != nil {
				names = append(names, *s.Name)
			}
		}
	}
	return names, nil
}

var _ provider.SecretsProvider = (*Client)(nil)
