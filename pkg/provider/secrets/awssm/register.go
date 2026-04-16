package awssm

import "github.com/getnvoi/nvoi/pkg/provider"

var Schema = provider.CredentialSchema{
	Name: "awssm",
	Fields: []provider.CredentialField{
		{Key: "access_key_id", Required: true, EnvVar: "AWS_ACCESS_KEY_ID", Flag: "access-key-id"},
		{Key: "secret_access_key", Required: true, EnvVar: "AWS_SECRET_ACCESS_KEY", Flag: "secret-access-key"},
		{Key: "region", Required: true, EnvVar: "AWS_REGION", Flag: "region"},
	},
}

func init() {
	provider.RegisterSecrets("awssm", Schema, func(creds map[string]string) provider.SecretsProvider {
		return New(creds)
	})
}
