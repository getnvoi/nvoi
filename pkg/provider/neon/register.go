package neon

import "github.com/getnvoi/nvoi/pkg/provider"

var Schema = provider.CredentialSchema{
	Name: "neon",
	Fields: []provider.CredentialField{
		{Key: "api_key", Required: true, EnvVar: "NEON_API_KEY", Flag: "api-key"},
		{Key: "base_url", Required: false, EnvVar: "NEON_BASE_URL", Flag: "base-url"},
	},
}

func init() {
	provider.RegisterDatabase("neon", Schema, func(creds map[string]string) provider.DatabaseProvider {
		return New(creds)
	})
}
