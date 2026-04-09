package handlers

import (
	"errors"

	"github.com/getnvoi/nvoi/pkg/managed"
)

// isMissingCredential checks if an error wraps managed.ErrMissingCredential.
func isMissingCredential(err error) bool {
	var e *managed.ErrMissingCredential
	return errors.As(err, &e)
}

// ── Shared input types for path params ───────────────────────────────────────

// WorkspaceScopedInput provides the workspace_id path param.
type WorkspaceScopedInput struct {
	WorkspaceID string `path:"workspace_id" format:"uuid" doc:"Workspace ID"`
}

// RepoScopedInput provides workspace_id + repo_id path params.
type RepoScopedInput struct {
	WorkspaceID string `path:"workspace_id" format:"uuid" doc:"Workspace ID"`
	RepoID      string `path:"repo_id" format:"uuid" doc:"Repo ID"`
}
