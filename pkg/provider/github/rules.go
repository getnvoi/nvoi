package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ruleset is one entry returned by GET /repos/{o}/{r}/rules/branches/{branch}.
// GitHub returns the flat list of active rules that apply to the branch —
// we only care whether ANY rule is present that would block a headless
// `PUT /contents/{path}` (pull_request, required_status_checks,
// non_fast_forward, update, deletion, etc.). The safe heuristic is
// "any ruleset at all" → treat the branch as protected, because the
// Contents API rejects the push long before we'd know which rule tripped.
type ruleset struct {
	Type string `json:"type"`
}

// defaultBranchProtected returns true when `branch` is covered by at
// least one active repository ruleset or by classic branch protection.
//
// Two mechanisms, queried in order:
//
//  1. Repository rulesets — the modern (2023+) replacement for classic
//     branch protection. Queried via `GET /repos/{o}/{r}/rules/branches/{b}`
//     which returns the *flattened* list of rules applying to the branch.
//     Any non-empty response ⇒ protected. Endpoint is public read
//     (doesn't require Administration:read); 403/404 fall through to the
//     classic check.
//
//  2. Classic branch protection — the legacy BranchProtection object on
//     the branch. Queried via `GET /repos/{o}/{r}/branches/{b}/protection`.
//     404 ⇒ unprotected. 200 ⇒ protected. 403 (token lacks
//     Administration:read) ⇒ we can't tell, so we err on the side of
//     safety and treat as protected, forcing the PR path.
//
// Returning true here short-circuits the direct-push attempt in
// CommitFiles; returning false lets CommitFiles try the direct push and
// fall back on `isProtectedBranchError` if the server still rejects it.
func (g *GitHubCI) defaultBranchProtected(ctx context.Context, branch string) (bool, error) {
	// 1. Rulesets — preferred check.
	var rules []ruleset
	rulesPath := fmt.Sprintf("%s/rules/branches/%s", repoPath(g.owner, g.repo), branch)
	err := g.http.Do(ctx, "GET", rulesPath, nil, &rules)
	switch {
	case err == nil:
		if len(rules) > 0 {
			return true, nil
		}
		// Empty list ⇒ no rulesets. Still check classic protection below.
	case provider.IsNotFound(err):
		// No rulesets configured on this repo — fall through.
	case isForbidden(err):
		// Token can read the repo but not the rulesets endpoint. Fall
		// through to classic protection; if that also 403s we assume
		// protected.
	default:
		return false, fmt.Errorf("github: check rulesets for %s: %w", branch, err)
	}

	// 2. Classic branch protection — legacy check.
	protectionPath := fmt.Sprintf("%s/branches/%s/protection", repoPath(g.owner, g.repo), branch)
	err = g.http.Do(ctx, "GET", protectionPath, nil, nil)
	switch {
	case err == nil:
		return true, nil
	case provider.IsNotFound(err):
		return false, nil
	case isForbidden(err):
		// Can't introspect — assume protected. Worst case we open a PR on
		// an unprotected branch, which is recoverable (the operator
		// merges it). The alternative (attempting a direct push that
		// fails mid-commit) is worse.
		return true, nil
	default:
		return false, fmt.Errorf("github: check branch protection for %s: %w", branch, err)
	}
}

// isProtectedBranchError recognizes the 409/422 responses the Contents API
// emits when a direct push is blocked by branch protection or a ruleset.
// Falls back on the error body text for older server variants that return
// a generic 422 without a machine-parseable code.
func isProtectedBranchError(err error) bool {
	apiErr, ok := err.(*utils.APIError)
	if !ok {
		return false
	}
	if apiErr.Status != 409 && apiErr.Status != 422 {
		return false
	}
	// Textual sniff — avoids a fragile enum match across GitHub server
	// releases. The server wording varies ("protected branch",
	// "repository rule violations", "Changes must be made through a pull
	// request") but every variant contains one of these fragments.
	body := strings.ToLower(apiErr.Body)
	switch {
	case strings.Contains(body, "protected branch"):
		return true
	case strings.Contains(body, "repository rule"):
		return true
	case strings.Contains(body, "pull request"):
		return true
	case strings.Contains(body, "required status check"):
		return true
	}
	return false
}

// isForbidden distinguishes 403 (permission) from 404 (not present). Used
// by defaultBranchProtected to decide whether to assume protected when
// the token lacks Administration:read.
func isForbidden(err error) bool {
	if apiErr, ok := err.(*utils.APIError); ok {
		return apiErr.Status == 403
	}
	return false
}
