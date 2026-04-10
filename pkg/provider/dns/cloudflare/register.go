package cloudflare

import "github.com/getnvoi/nvoi/pkg/provider"

var DNSSchema = provider.CredentialSchema{
	Name: "cloudflare",
	Fields: []provider.CredentialField{
		{Key: "api_key", Required: true, EnvVar: "CF_API_KEY", Flag: "api-key"},
		{Key: "zone_id", Required: true, EnvVar: "CF_ZONE_ID", Flag: "zone-id"},
		{Key: "zone", Required: true, EnvVar: "DNS_ZONE", Flag: "zone"},
	},
}

func init() {
	provider.RegisterDNS("cloudflare", DNSSchema, func(creds map[string]string) provider.DNSProvider {
		return NewDNS(creds)
	})
}
