package cloudflare

import (
	"github.com/getnvoi/nvoi/pkg/provider"
)

var Schema = provider.CredentialSchema{
	Name: "cloudflare tunnel",
	Fields: []provider.CredentialField{
		{Key: "api_token", Required: true, EnvVar: "CF_API_KEY", Flag: "cloudflare-api-token"},
		{Key: "account_id", Required: true, EnvVar: "CF_ACCOUNT_ID", Flag: "cloudflare-account-id"},
	},
}

func init() {
	provider.RegisterTunnel("cloudflare", Schema, func(creds map[string]string) provider.TunnelProvider {
		return NewClient(creds)
	})
}
