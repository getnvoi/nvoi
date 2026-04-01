package cloudflare

import "github.com/getnvoi/nvoi/internal/provider"

var Schema = provider.CredentialSchema{
	Name: "cloudflare",
	Fields: []provider.CredentialField{
		{Key: "api_key", Required: true, EnvVar: "CF_API_KEY", Flag: "api-key"},
		{Key: "account_id", Required: true, EnvVar: "CF_ACCOUNT_ID", Flag: "account-id"},
	},
}

func init() {
	provider.RegisterBucket("cloudflare", Schema, func(creds map[string]string) provider.BucketProvider {
		return New(creds)
	})
}
