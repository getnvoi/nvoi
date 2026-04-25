// Package github is the CIProvider implementation for GitHub Actions.
// Registered as "github" under providers.ci.
//
// Credentials:
//   - GITHUB_TOKEN (required) — a classic PAT or fine-grained PAT with the
//     following scopes on the target repo:
//     · Actions: read/write   (secrets.read is NOT sufficient — sealing
//     requires the public key from the same endpoint family)
//     · Contents: read/write  (write the workflow file + create branches)
//     · Pull requests: write  (fallback flow when branch protection
//     blocks direct push to the default branch)
//     · Administration: read  (detect repository rulesets)
//   - GITHUB_REPO (optional) — `owner/repo` override. When absent, the
//     cmd/cli/ci.go boundary infers it from `git remote get-url origin`
//     and injects it into the creds map before ResolveCI runs. Exposing
//     it as an override lets operators onboard repos whose default remote
//     isn't GitHub (mirrors, multi-remote setups).
//
// The provider is entirely self-contained — uses the REST API for every
// operation (secrets, workflow commit, ruleset detection, PR creation).
// There is no git CLI dependency on the operator's machine.
package github

import "github.com/getnvoi/nvoi/pkg/provider"

func init() {
	provider.RegisterCI(
		"github",
		provider.CredentialSchema{
			Name: "github",
			Fields: []provider.CredentialField{
				{Key: "token", Required: true, EnvVar: "GITHUB_TOKEN", Flag: "github-token"},
				{Key: "repo", Required: false, EnvVar: "GITHUB_REPO", Flag: "github-repo"},
			},
		},
		func(creds map[string]string) provider.CIProvider {
			return New(creds["token"], creds["repo"])
		},
	)
}
