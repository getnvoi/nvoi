package scaleway

import "github.com/getnvoi/nvoi/pkg/provider"

var BucketSchema = provider.CredentialSchema{
	Name: "scaleway",
	Fields: []provider.CredentialField{
		{Key: "access_key", Required: true, EnvVar: "SCW_ACCESS_KEY", Flag: "access-key"},
		{Key: "secret_key", Required: true, EnvVar: "SCW_SECRET_KEY", Flag: "secret-key"},
		{Key: "region", Required: true, EnvVar: "SCW_REGION", Flag: "region"},
	},
}

func init() {
	provider.RegisterBucket("scaleway", BucketSchema, func(creds map[string]string) provider.BucketProvider {
		return NewBucket(creds)
	})
}
