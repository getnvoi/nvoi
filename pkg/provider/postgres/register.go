package postgres

import "github.com/getnvoi/nvoi/pkg/provider"

func init() {
	provider.RegisterDatabase("postgres", provider.CredentialSchema{Name: "postgres"}, func(creds map[string]string) provider.DatabaseProvider {
		return &Provider{}
	})
}
