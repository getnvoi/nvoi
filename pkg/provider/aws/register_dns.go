package aws

import "github.com/getnvoi/nvoi/pkg/provider"

var DNSSchema = provider.CredentialSchema{
	Name: "aws",
	Fields: []provider.CredentialField{
		{Key: "access_key_id", Required: true, EnvVar: "AWS_ACCESS_KEY_ID", Flag: "access-key-id"},
		{Key: "secret_access_key", Required: true, EnvVar: "AWS_SECRET_ACCESS_KEY", Flag: "secret-access-key"},
		{Key: "region", Required: true, EnvVar: "AWS_REGION", Flag: "region"},
		{Key: "zone", Required: true, EnvVar: "DNS_ZONE", Flag: "zone"},
	},
}

func init() {
	provider.RegisterDNS("aws", DNSSchema, func(creds map[string]string) provider.DNSProvider {
		return NewDNS(creds)
	})
}
