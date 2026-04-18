// Package aws implements provider.InfraProvider against the AWS API using
// the AWS SDK v2. Resources identified by tag:Name. VPC networking is
// multi-step (VPC + subnet + IGW + route table + association).
package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/getnvoi/nvoi/pkg/provider/awsbase"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Client talks to the AWS EC2 API.
type Client struct {
	ec2       *ec2.Client
	region    string
	configErr error // non-nil if LoadDefaultConfig failed

	// shell caches the SSH connection across Bootstrap → NodeShell →
	// end-of-deploy. See infra.go for the cache lifecycle.
	shell utils.SSHClient
}

// New creates an AWS compute client from a credentials map.
func New(creds map[string]string) *Client {
	cfg, err := awsbase.LoadConfig(creds)
	if err != nil {
		return &Client{configErr: err}
	}
	return &Client{
		ec2:    ec2.NewFromConfig(cfg),
		region: creds["region"],
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

// Compile-time satisfaction lives in infra.go (var _ provider.InfraProvider).
