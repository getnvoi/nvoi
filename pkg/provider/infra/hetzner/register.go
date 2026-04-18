package hetzner

import "github.com/getnvoi/nvoi/pkg/provider"

var Schema = provider.CredentialSchema{
	Name: "hetzner",
	Fields: []provider.CredentialField{
		{Key: "token", Required: true, EnvVar: "HETZNER_TOKEN", Flag: "token"},
	},
}

func init() {
	// Both registries point at the same *Client during the InfraProvider
	// rollout. RegisterCompute keeps reconcile / pkg/core's legacy path
	// working until C6 swaps it out; RegisterInfra is the new surface that
	// downstream code switches to. RegisterCompute is removed in C10
	// alongside the ComputeProvider interface deletion.
	factory := func(creds map[string]string) *Client { return New(creds["token"]) }
	provider.RegisterCompute("hetzner", Schema, func(creds map[string]string) provider.ComputeProvider {
		return factory(creds)
	})
	provider.RegisterInfra("hetzner", Schema, func(creds map[string]string) provider.InfraProvider {
		return factory(creds)
	})
}
