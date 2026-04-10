package scaleway

import "github.com/getnvoi/nvoi/pkg/provider"

var DNSSchema = provider.CredentialSchema{
	Name: "scaleway",
	Fields: []provider.CredentialField{
		{Key: "secret_key", Required: true, EnvVar: "SCW_SECRET_KEY", Flag: "secret-key"},
		{Key: "zone", Required: true, EnvVar: "DNS_ZONE", Flag: "zone"},
	},
}

func init() {
	provider.RegisterDNS("scaleway", DNSSchema, func(creds map[string]string) provider.DNSProvider {
		return NewDNS(creds)
	})
}
