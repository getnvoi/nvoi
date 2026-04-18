package aws

import "github.com/getnvoi/nvoi/pkg/provider"

var ComputeSchema = provider.CredentialSchema{
	Name: "aws",
	Fields: []provider.CredentialField{
		{Key: "access_key_id", Required: true, EnvVar: "AWS_ACCESS_KEY_ID", Flag: "access-key-id"},
		{Key: "secret_access_key", Required: true, EnvVar: "AWS_SECRET_ACCESS_KEY", Flag: "secret-access-key"},
		{Key: "region", Required: true, EnvVar: "AWS_REGION", Flag: "region"},
	},
}

func init() {
	// Both registries point at the same *Client during the InfraProvider
	// rollout. RegisterCompute keeps reconcile / pkg/core's legacy path
	// working until C6 swaps it out. RegisterCompute is removed in C10
	// alongside the ComputeProvider interface deletion.
	factory := func(creds map[string]string) *Client { return New(creds) }
	provider.RegisterCompute("aws", ComputeSchema, func(creds map[string]string) provider.ComputeProvider {
		return factory(creds)
	})
	provider.RegisterInfra("aws", ComputeSchema, func(creds map[string]string) provider.InfraProvider {
		return factory(creds)
	})
}
