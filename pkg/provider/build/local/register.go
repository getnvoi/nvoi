package local

import "github.com/getnvoi/nvoi/pkg/provider"

func init() {
	provider.RegisterBuild("local", provider.CredentialSchema{
		Name:   "local",
		Fields: nil,
	}, func(creds map[string]string) provider.BuildProvider {
		return &Builder{}
	})
}
