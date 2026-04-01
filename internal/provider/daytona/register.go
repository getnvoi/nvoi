package daytona

import "github.com/getnvoi/nvoi/internal/provider"

func init() {
	provider.RegisterBuild("daytona", provider.CredentialSchema{
		Name: "daytona",
		Fields: []provider.CredentialField{
			{Key: "api_key", Required: true, EnvVar: "DAYTONA_API_KEY", Flag: "api-key"},
		},
	}, func(creds map[string]string) provider.BuildProvider {
		return NewBuilder(creds["api_key"])
	})
}
