package infisical

import "github.com/getnvoi/nvoi/pkg/provider"

var Schema = provider.CredentialSchema{
	Name: "infisical",
	Fields: []provider.CredentialField{
		{Key: "token", Required: true, EnvVar: "INFISICAL_TOKEN", Flag: "token"},
		{Key: "host", Required: false, EnvVar: "INFISICAL_HOST", Flag: "host"},
		{Key: "project_id", Required: true, EnvVar: "INFISICAL_PROJECT_ID", Flag: "project-id"},
		{Key: "environment", Required: false, EnvVar: "INFISICAL_ENVIRONMENT", Flag: "environment"},
	},
}

func init() {
	provider.RegisterSecrets("infisical", Schema, func(creds map[string]string) provider.SecretsProvider {
		return New(creds)
	})
}
