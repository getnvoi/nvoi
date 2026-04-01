package cloudflare

import "github.com/getnvoi/nvoi/internal/provider"

var BucketSchema = provider.CredentialSchema{
	Name: "cloudflare",
	Fields: []provider.CredentialField{
		{Key: "api_key", Required: true, EnvVar: "CF_API_KEY", Flag: "api-key"},
		{Key: "account_id", Required: true, EnvVar: "CF_ACCOUNT_ID", Flag: "account-id"},
	},
}

var DNSSchema = provider.CredentialSchema{
	Name: "cloudflare",
	Fields: []provider.CredentialField{
		{Key: "api_key", Required: true, EnvVar: "CF_API_KEY", Flag: "api-key"},
		{Key: "zone_id", Required: true, EnvVar: "CF_ZONE_ID", Flag: "zone-id"},
	},
}

func init() {
	provider.RegisterBucket("cloudflare", BucketSchema, func(creds map[string]string) provider.BucketProvider {
		return New(creds)
	})
	provider.RegisterDNS("cloudflare", DNSSchema, func(creds map[string]string) provider.DNSProvider {
		return NewDNS(creds)
	})
}
