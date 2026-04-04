// Package aws implements provider.ComputeProvider and provider.DNSProvider
// against the AWS API using the AWS SDK v2. Resources identified by tag:Name.
// VPC networking is multi-step (VPC + subnet + IGW + route table + association).
package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// Client talks to the AWS EC2 API.
type Client struct {
	ec2       *ec2.Client
	region    string
	configErr error // non-nil if LoadDefaultConfig failed
}

// New creates an AWS compute client from a credentials map.
func New(creds map[string]string) *Client {
	region := creds["region"]
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds["access_key_id"],
			creds["secret_access_key"],
			"",
		)),
	)
	if err != nil {
		return &Client{configErr: fmt.Errorf("aws: load config: %w", err)}
	}
	return &Client{
		ec2:    ec2.NewFromConfig(cfg),
		region: region,
	}
}

// ValidateCredentials checks the API key by listing regions.
func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.configErr != nil {
		return c.configErr
	}
	_, err := c.ec2.DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		return fmt.Errorf("aws: invalid credentials: %w", err)
	}
	return nil
}

// ArchForType returns the CPU architecture for an AWS instance type.
// Graviton families (a1, t4g, m6g, c6g, r6g, m7g, c7g, r7g, etc.) are arm64.
func (c *Client) ArchForType(instanceType string) string {
	family := strings.Split(instanceType, ".")[0]
	if strings.HasSuffix(family, "g") || strings.HasPrefix(family, "a1") {
		return "arm64"
	}
	return "amd64"
}

// nameTag builds a tag:Name filter for DescribeX calls.
func nameTag(name string) []ec2types.Filter {
	return []ec2types.Filter{
		{Name: aws.String("tag:Name"), Values: []string{name}},
	}
}

// nvoiTags builds a tag list for resource creation.
func nvoiTags(name string, labels map[string]string) []ec2types.Tag {
	tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String(name)},
	}
	for k, v := range labels {
		tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return tags
}

func tagSpec(resourceType ec2types.ResourceType, name string, labels map[string]string) []ec2types.TagSpecification {
	return []ec2types.TagSpecification{
		{ResourceType: resourceType, Tags: nvoiTags(name, labels)},
	}
}

// newRoute53Client creates a Route53 client from the same credentials.
// Returns (client, error) — caller must store and check the error.
func newRoute53Client(creds map[string]string) (*route53.Client, error) {
	region := creds["region"]
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds["access_key_id"],
			creds["secret_access_key"],
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("aws: load route53 config: %w", err)
	}
	return route53.NewFromConfig(cfg), nil
}

var _ provider.ComputeProvider = (*Client)(nil)
