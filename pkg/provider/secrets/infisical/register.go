package infisical

import "github.com/getnvoi/nvoi/pkg/provider"

var Schema = provider.CredentialSchema{
	Name: "infisical",
	Fields: []provider.CredentialField{
		{Key: "client_id", Required: true, EnvVar: "INFISICAL_CLIENT_ID", Flag: "client-id"},
		{Key: "client_secret", Required: true, EnvVar: "INFISICAL_CLIENT_SECRET", Flag: "client-secret"},
		{Key: "project_slug", Required: true, EnvVar: "INFISICAL_PROJECT_SLUG", Flag: "project-slug"},
		{Key: "environment", Required: false, EnvVar: "INFISICAL_ENVIRONMENT", Flag: "environment"},
		{Key: "host", Required: false, EnvVar: "INFISICAL_HOST", Flag: "host"},
	},
}

func init() {
	provider.RegisterSecrets("infisical", Schema, func(creds map[string]string) provider.SecretsProvider {
		return New(creds)
	})
}
