package scaleway

import "github.com/getnvoi/nvoi/pkg/provider"

// Schema is the Scaleway credential schema. Renamed from ComputeSchema
// in C10 — the "compute" name predated the InfraProvider rename.
var Schema = provider.CredentialSchema{
	Name: "scaleway",
	Fields: []provider.CredentialField{
		{Key: "secret_key", Required: true, EnvVar: "SCW_SECRET_KEY", Flag: "secret-key"},
		{Key: "project_id", Required: true, EnvVar: "SCW_DEFAULT_ORGANIZATION_ID", Flag: "project-id"},
	},
}

func init() {
	provider.RegisterInfra("scaleway", Schema, func(creds map[string]string) provider.InfraProvider {
		return New(creds)
	})
}
