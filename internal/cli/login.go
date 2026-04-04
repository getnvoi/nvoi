package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

type loginResponse struct {
	Token string `json:"token"`
	User  struct {
		ID             string `json:"id"`
		GithubUsername string `json:"github_username"`
	} `json:"user"`
	IsNew bool `json:"is_new"`
}

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Authenticate with nvoi",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin()
		},
	}
}

func runLogin() error {
	pat, source := resolveGitHubToken()
	if pat == "" {
		return fmt.Errorf("no github token — install gh CLI, set GITHUB_TOKEN, or paste a token")
	}
	fmt.Printf("using token from %s\n", source)

	client := NewUnauthClient()

	var resp loginResponse
	err := client.Do("POST", "/login", map[string]string{
		"github_token": pat,
	}, &resp)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	if err := SaveAuthConfig(&AuthConfig{
		APIBase:  client.base,
		Token:    resp.Token,
		Username: resp.User.GithubUsername,
	}); err != nil {
		return fmt.Errorf("save auth: %w", err)
	}

	if resp.IsNew {
		fmt.Printf("welcome, %s!\n", resp.User.GithubUsername)
	} else {
		fmt.Printf("logged in as %s\n", resp.User.GithubUsername)
	}

	return nil
}

// resolveGitHubToken tries three sources in order:
// 1. gh auth token (GitHub CLI)
// 2. GITHUB_TOKEN env var
// 3. interactive prompt
func resolveGitHubToken() (token, source string) {
	// 1. gh CLI
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		t := strings.TrimSpace(string(out))
		if t != "" {
			return t, "GitHub CLI"
		}
	}

	// 2. env var
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t, "GITHUB_TOKEN"
	}

	// 3. prompt
	fmt.Print("GitHub personal access token: ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	t := strings.TrimSpace(line)
	if t != "" {
		return t, "manual input"
	}

	return "", ""
}
