package daytona

import "github.com/getnvoi/nvoi/pkg/provider"

// init registers "daytona" as a BuildProvider. Matches 9b39daf's
// registration shape exactly (one credential: api_key from DAYTONA_API_KEY),
// with the new BuildCapability block: RequiresBuilders=false because
// daytona runs inside a managed sandbox, not on a role:builder server.
// Validator R1 rejects any config that pairs `build: daytona` with
// `servers[].role: builder` entries (unused infra).
func init() {
	provider.RegisterBuild(
		"daytona",
		provider.CredentialSchema{
			Name: "daytona",
			Fields: []provider.CredentialField{
				{Key: "api_key", Required: true, EnvVar: "DAYTONA_API_KEY", Flag: "api-key"},
			},
		},
		provider.BuildCapability{
			RequiresBuilders: false,
		},
		func(creds map[string]string) provider.BuildProvider {
			return New(creds["api_key"])
		},
	)
}
