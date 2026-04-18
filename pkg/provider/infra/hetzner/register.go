package hetzner

import "github.com/getnvoi/nvoi/pkg/provider"

var Schema = provider.CredentialSchema{
	Name: "hetzner",
	Fields: []provider.CredentialField{
		{Key: "token", Required: true, EnvVar: "HETZNER_TOKEN", Flag: "token"},
	},
}

func init() {
	provider.RegisterInfra("hetzner", Schema, func(creds map[string]string) provider.InfraProvider {
		return New(creds["token"])
	})
}
