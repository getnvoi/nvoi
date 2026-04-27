package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
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
// the exact tree being deployed onto the remote build host.
//
// `--auto-approve` (alias `-y`) skips the plan prompt. `--ci` (root
// flag) implies `--auto-approve` — CI runs would otherwise hang on the
// confirmation prompt.
func newDeployCmd(rt *runtime) *cobra.Command {
	var autoApprove bool
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy from config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			rt.dc.GitRemote = gitOrigin(ctx)
			rt.dc.GitRef = gitHeadSHA(ctx)

			// --ci implies auto-approve so CI runs don't hang on prompt.
			ciFlag, _ := cmd.Root().PersistentFlags().GetBool("ci")
			if ciFlag {
				autoApprove = true
			}

			return reconcile.Deploy(ctx, rt.dc, rt.cfg,
				reconcile.WithOnPlan(func(plan *reconcile.Plan) (bool, error) {
					render.RenderPlan(plan)
					if autoApprove || len(plan.Promptable()) == 0 {
						return true, nil
					}
					if !isStdinTTY() {
						return false, fmt.Errorf("plan has %d change(s) requiring confirmation; pass --auto-approve / -y, or --ci", len(plan.Promptable()))
					}
					return promptYes("Continue? [y/N]: "), nil
				}),
			)
		},
	}
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "skip the plan confirmation prompt (CI uses --ci which implies this)")
	return cmd
}

// promptYes reads one line from stdin and returns true on "y" / "yes"
// (case-insensitive). Empty input → false (default no, matches the
// `[y/N]` capitalization convention).
func promptYes(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// isStdinTTY reports whether stdin is a character device. Used to
// distinguish interactive runs (operator at a terminal) from
// pipes / redirects (CI, scripts) where prompting would hang.
func isStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
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
