// Package awsbase provides shared AWS SDK v2 config loading.
// Used by compute/aws, dns/aws, and storage/aws.
package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// LoadConfig creates an aws.Config from a credentials map.
// Expected keys: "access_key_id", "secret_access_key", "region".
func LoadConfig(creds map[string]string) (aws.Config, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(creds["region"]),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds["access_key_id"],
			creds["secret_access_key"],
			"",
		)),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("aws: load config: %w", err)
	}
	return cfg, nil
}
