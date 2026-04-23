package planetscale

import "github.com/getnvoi/nvoi/pkg/provider"

var Schema = provider.CredentialSchema{
	Name: "planetscale",
	Fields: []provider.CredentialField{
		{Key: "service_token", Required: true, EnvVar: "PLANETSCALE_SERVICE_TOKEN", Flag: "service-token"},
		{Key: "organization", Required: true, EnvVar: "PLANETSCALE_ORG", Flag: "organization"},
		{Key: "base_url", Required: false, EnvVar: "PLANETSCALE_BASE_URL", Flag: "base-url"},
	},
}

func init() {
	provider.RegisterDatabase("planetscale", Schema, func(creds map[string]string) provider.DatabaseProvider {
		return New(creds)
	})
}
