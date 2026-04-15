package infisical

import (
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

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

// BootstrapKeys are the credential keys written to the ESO bootstrap k8s Secret.
var BootstrapKeys = []string{"client_id", "client_secret"}

func init() {
	provider.RegisterSecrets("infisical", Schema, func(creds map[string]string) provider.SecretsProvider {
		return New(creds)
	})

	kube.RegisterESOProvider("infisical", func(authName string, creds map[string]string) map[string]any {
		host := creds["host"]
		if host == "" {
			host = "https://app.infisical.com"
		}
		env := creds["environment"]
		if env == "" {
			env = "production"
		}
		return map[string]any{
			"infisical": map[string]any{
				"hostAPI": host,
				"auth": map[string]any{
					"universalAuthCredentials": map[string]any{
						"clientId": map[string]any{
							"name": authName,
							"key":  "client_id",
						},
						"clientSecret": map[string]any{
							"name": authName,
							"key":  "client_secret",
						},
					},
				},
				"secretsScope": map[string]any{
					"projectSlug":     creds["project_slug"],
					"environmentSlug": env,
				},
			},
		}
	})
}
