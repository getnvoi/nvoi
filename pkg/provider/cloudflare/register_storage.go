package cloudflare

import "github.com/getnvoi/nvoi/pkg/provider"

var BucketSchema = provider.CredentialSchema{
	Name: "cloudflare",
	Fields: []provider.CredentialField{
		{Key: "api_key", Required: true, EnvVar: "CF_API_KEY", Flag: "api-key"},
		{Key: "account_id", Required: true, EnvVar: "CF_ACCOUNT_ID", Flag: "account-id"},
	},
}

func init() {
	provider.RegisterBucket("cloudflare", BucketSchema, func(creds map[string]string) provider.BucketProvider {
		return NewBucket(creds)
	})
}
