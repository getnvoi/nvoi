package scaleway

import "github.com/getnvoi/nvoi/pkg/provider"

var ComputeSchema = provider.CredentialSchema{
	Name: "scaleway",
	Fields: []provider.CredentialField{
		{Key: "secret_key", Required: true, EnvVar: "SCW_SECRET_KEY", Flag: "secret-key"},
		{Key: "project_id", Required: true, EnvVar: "SCW_DEFAULT_ORGANIZATION_ID", Flag: "project-id"},
	},
}

func init() {
	provider.RegisterCompute("scaleway", ComputeSchema, func(creds map[string]string) provider.ComputeProvider {
		return New(creds)
	})
}
