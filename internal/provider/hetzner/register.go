package hetzner

import "github.com/getnvoi/nvoi/internal/provider"

var Schema = provider.CredentialSchema{
	Name: "hetzner",
	Fields: []provider.CredentialField{
		{Key: "token", Required: true, EnvVar: "HETZNER_TOKEN", Flag: "token"},
	},
}

func init() {
	provider.RegisterCompute("hetzner", Schema, func(creds map[string]string) provider.ComputeProvider {
		return New(creds["token"])
	})
}
