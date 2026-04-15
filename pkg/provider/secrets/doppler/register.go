package doppler

import (
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

var Schema = provider.CredentialSchema{
	Name: "doppler",
	Fields: []provider.CredentialField{
		{Key: "token", Required: true, EnvVar: "DOPPLER_TOKEN", Flag: "token"},
		{Key: "project", Required: false, EnvVar: "DOPPLER_PROJECT", Flag: "project"},
		{Key: "config", Required: false, EnvVar: "DOPPLER_CONFIG", Flag: "config"},
	},
}

// BootstrapKeys are the credential keys written to the ESO bootstrap k8s Secret.
var BootstrapKeys = []string{"token"}

func init() {
	provider.RegisterSecrets("doppler", Schema, func(creds map[string]string) provider.SecretsProvider {
		return New(creds)
	})

	kube.RegisterESOProvider("doppler", func(authName string, _ map[string]string) map[string]any {
		return map[string]any{
			"doppler": map[string]any{
				"auth": map[string]any{
					"secretRef": map[string]any{
						"dopplerToken": map[string]any{
							"name": authName,
							"key":  "token",
						},
					},
				},
			},
		}
	})
}
