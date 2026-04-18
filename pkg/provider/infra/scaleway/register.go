package scaleway

import "github.com/getnvoi/nvoi/pkg/provider"

var ComputeSchema = provider.CredentialSchema{
	Name: "scaleway",
	Fields: []provider.CredentialField{
		{Key: "secret_key", Required: true, EnvVar: "SCW_SECRET_KEY", Flag: "secret-key"},
		{Key: "project_id", Required: true, EnvVar: "SCW_DEFAULT_ORGANIZATION_ID", Flag: "project-id"},
	},
}

func init() {
	// Both registries point at the same *Client during the InfraProvider
	// rollout. RegisterCompute keeps reconcile / pkg/core's legacy path
	// working until C6 swaps it out. RegisterCompute is removed in C10.
	factory := func(creds map[string]string) *Client { return New(creds) }
	provider.RegisterCompute("scaleway", ComputeSchema, func(creds map[string]string) provider.ComputeProvider {
		return factory(creds)
	})
	provider.RegisterInfra("scaleway", ComputeSchema, func(creds map[string]string) provider.InfraProvider {
		return factory(creds)
	})
}
