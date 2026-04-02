package github

import "github.com/getnvoi/nvoi/pkg/provider"

func init() {
	provider.RegisterBuild("github", provider.CredentialSchema{
		Name:   "github",
		Fields: nil, // uses GITHUB_TOKEN from git auth (req.GitToken)
	}, func(creds map[string]string) provider.BuildProvider {
		return &Builder{}
	})
}
