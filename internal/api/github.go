package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// GitHubUser is the subset of fields we need from the GitHub API.
type GitHubUser struct {
	Login string `json:"login"`
}

// GitHubVerifier verifies a GitHub PAT and returns the authenticated user.
// Abstracted as a function type so tests can swap it out.
type GitHubVerifier func(pat string) (*GitHubUser, error)

// VerifyGitHubPAT calls the real GitHub API to verify a personal access token.
func VerifyGitHubPAT(pat string) (*GitHubUser, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %d", resp.StatusCode)
	}

	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode github response: %w", err)
	}
	if user.Login == "" {
		return nil, fmt.Errorf("github returned empty login")
	}
	return &user, nil
}
