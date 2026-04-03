package scaleway

import "github.com/getnvoi/nvoi/pkg/provider"

var ComputeSchema = provider.CredentialSchema{
	Name: "scaleway",
	Fields: []provider.CredentialField{
		{Key: "secret_key", Required: true, EnvVar: "SCW_SECRET_KEY", Flag: "secret-key"},
		{Key: "project_id", Required: true, EnvVar: "SCW_DEFAULT_ORGANIZATION_ID", Flag: "project-id"},
	},
}

var DNSSchema = provider.CredentialSchema{
	Name: "scaleway",
	Fields: []provider.CredentialField{
		{Key: "secret_key", Required: true, EnvVar: "SCW_SECRET_KEY", Flag: "secret-key"},
		{Key: "zone", Required: true, EnvVar: "DNS_ZONE", Flag: "zone"},
	},
}

func init() {
	provider.RegisterCompute("scaleway", ComputeSchema, func(creds map[string]string) provider.ComputeProvider {
		return New(creds)
	})
	provider.RegisterDNS("scaleway", DNSSchema, func(creds map[string]string) provider.DNSProvider {
		return NewDNS(creds)
	})
}
