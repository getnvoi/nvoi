package main

import (
	"context"
	"os/exec"
	"strings"

	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/spf13/cobra"
)

// newDeployCmd wires `nvoi deploy`. Always calls reconcile.Deploy — the
// BuildProvider family (local / ssh / daytona) is resolved inside the
// reconciler per service during BuildImages. There is no remote-dispatch
// branch on this side: each BuildProvider is self-contained, owns its own
// transport, and returns a pullable image ref.
//
// Before calling Deploy, we infer the operator's git checkout via
// `git remote get-url origin` + `git rev-parse HEAD` and stash both on
// DeployContext. Remote builders (ssh, daytona) need this to `git clone`
// the exact tree being deployed onto the remote build host. Local
// builders ignore it. Empty values (cwd not a git checkout) surface as a
// hard error inside the remote builder's validateBuildRequest — no silent
// fallback to a possibly-stale remote default branch.
func newDeployCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy from config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			rt.dc.GitRemote = gitOrigin(ctx)
			rt.dc.GitRef = gitHeadSHA(ctx)
			return reconcile.Deploy(ctx, rt.dc, rt.cfg)
		},
	}
}

// gitOrigin returns the URL of the operator's `origin` remote, or "" when
// the cwd is not a git checkout / has no origin. Silent failure — callers
// that need it (remote BuildProviders) error with an actionable message
// when the field is empty; `local` never reads it.
func gitOrigin(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitHeadSHA returns the full 40-char SHA of HEAD, or "" when the cwd is
// not a git checkout. Same silent-failure contract as gitOrigin.
func gitHeadSHA(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
