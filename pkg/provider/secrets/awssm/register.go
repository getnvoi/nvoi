package awssm

import (
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

var Schema = provider.CredentialSchema{
	Name: "awssm",
	Fields: []provider.CredentialField{
		{Key: "access_key_id", Required: true, EnvVar: "AWS_ACCESS_KEY_ID", Flag: "access-key-id"},
		{Key: "secret_access_key", Required: true, EnvVar: "AWS_SECRET_ACCESS_KEY", Flag: "secret-access-key"},
		{Key: "region", Required: true, EnvVar: "AWS_REGION", Flag: "region"},
	},
}

// BootstrapKeys are the credential keys written to the ESO bootstrap k8s Secret.
var BootstrapKeys = []string{"access_key_id", "secret_access_key"}

func init() {
	provider.RegisterSecrets("awssm", Schema, func(creds map[string]string) provider.SecretsProvider {
		return New(creds)
	})

	kube.RegisterESOProvider("awssm", func(authName string, creds map[string]string) map[string]any {
		region := creds["region"]
		if region == "" {
			region = "us-east-1"
		}
		return map[string]any{
			"aws": map[string]any{
				"service": "SecretsManager",
				"region":  region,
				"auth": map[string]any{
					"secretRef": map[string]any{
						"accessKeyIDSecretRef": map[string]any{
							"name": authName,
							"key":  "access_key_id",
						},
						"secretAccessKeySecretRef": map[string]any{
							"name": authName,
							"key":  "secret_access_key",
						},
					},
				},
			},
		}
	})
}
