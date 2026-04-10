package github

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestResolveBuild_Registered(t *testing.T) {
	// github provider has no required credentials (uses GITHUB_TOKEN from req)
	p, err := provider.ResolveBuild("github", map[string]string{})
	if err != nil {
		t.Fatalf("ResolveBuild: %v", err)
	}
	if p == nil {
		t.Fatal("ResolveBuild returned nil")
	}
}

func TestParseRepo(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{"org/repo", "org", "repo", false},
		{"https://github.com/org/repo.git", "org", "repo", false},
		{"https://github.com/org/repo", "org", "repo", false},
		{"git@github.com:org/repo.git", "org", "repo", false},
		{"http://github.com/org/repo.git", "org", "repo", false},
		// With path beyond owner/repo
		{"https://github.com/org/repo/tree/main", "org", "repo", false},
		// Invalid
		{"noslash", "", "", true},
		{"", "", "", true},
		{"/leading", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			owner, repo, err := parseRepo(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRepo(%q): %v", tt.input, err)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

func TestWorkflowPath(t *testing.T) {
	if workflowPath != ".github/workflows/nvoi-build.yml" {
		t.Errorf("workflowPath = %q, want %q", workflowPath, ".github/workflows/nvoi-build.yml")
	}
}
