package doppler

import "github.com/getnvoi/nvoi/pkg/provider"

var Schema = provider.CredentialSchema{
	Name: "doppler",
	Fields: []provider.CredentialField{
		{Key: "token", Required: true, EnvVar: "DOPPLER_TOKEN", Flag: "token"},
		{Key: "project", Required: false, EnvVar: "DOPPLER_PROJECT", Flag: "project"},
		{Key: "config", Required: false, EnvVar: "DOPPLER_CONFIG", Flag: "config"},
	},
}

func init() {
	provider.RegisterSecrets("doppler", Schema, func(creds map[string]string) provider.SecretsProvider {
		return New(creds)
	})
}
