package aws

import "github.com/getnvoi/nvoi/pkg/provider"

// Schema is the AWS credential schema. Renamed from ComputeSchema in
// C10 — the "compute" name predated the InfraProvider rename.
var Schema = provider.CredentialSchema{
	Name: "aws",
	Fields: []provider.CredentialField{
		{Key: "access_key_id", Required: true, EnvVar: "AWS_ACCESS_KEY_ID", Flag: "access-key-id"},
		{Key: "secret_access_key", Required: true, EnvVar: "AWS_SECRET_ACCESS_KEY", Flag: "secret-access-key"},
		{Key: "region", Required: true, EnvVar: "AWS_REGION", Flag: "region"},
	},
}

func init() {
	provider.RegisterInfra("aws", Schema, func(creds map[string]string) provider.InfraProvider {
		return New(creds)
	})
}
