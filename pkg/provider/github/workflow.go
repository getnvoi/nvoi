package github

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// workflowPath is where the rendered workflow lands in the repo.
const workflowPath = ".github/workflows/nvoi.yml"

// defaultNvoiVersion is used when CIWorkflowPlan.NvoiVersion is empty.
// Pinning to "latest" would make deploys non-reproducible — the runner
// would fetch a different binary every push. Callers that want a pinned
// version set NvoiVersion explicitly; the validator warns on empty.
const defaultNvoiVersion = "latest"

// bt is a literal backtick. Go raw strings can't contain backticks and
// doubled-up "`"+`…`+"`" concatenation is noisy; this one-character const
// keeps the template below readable.
const bt = "`"

// renderWorkflow emits the GitHub Actions workflow that runs `nvoi
// deploy` on every push to the repo's default branch. Deterministic —
// same input → byte-identical output — so repeated `ci init` runs don't
// touch the file unless the plan actually changed.
func renderWorkflow(plan provider.CIWorkflowPlan) (string, []byte, error) {
	version := strings.TrimSpace(plan.NvoiVersion)
	if version == "" {
		version = defaultNvoiVersion
	}

	// Env block — every SecretEnv entry gets mapped from ${{ secrets.X }}.
	// Ordered per plan.SecretEnv; caller is responsible for a stable
	// ordering (cmd/cli/ci.go sorts). Deterministic output is essential
	// for idempotent commits.
	var envBlock bytes.Buffer
	for _, name := range plan.SecretEnv {
		if strings.TrimSpace(name) == "" {
			return "", nil, fmt.Errorf("render workflow: empty SecretEnv entry")
		}
		fmt.Fprintf(&envBlock, "          %s: ${{ secrets.%s }}\n", name, name)
	}

	content := fmt.Sprintf(`# Managed by `+bt+`nvoi ci init`+bt+` — DO NOT EDIT BY HAND.
#
# This workflow runs on every push to the default branch and downloads
# a pinned nvoi binary to run `+bt+`nvoi deploy`+bt+` against the cluster described
# in `+bt+`nvoi.yaml`+bt+`.
#
# Secrets in the env: block below are managed by `+bt+`nvoi ci init`+bt+` and
# are synced from the operator's local environment at init time.
# Re-run `+bt+`nvoi ci init`+bt+` to rotate secrets or pick up new ones.

name: nvoi deploy

on:
  push:
    branches: ["**"]
  pull_request:
    branches: ["**"]

concurrency:
  group: nvoi-deploy-${{ github.ref }}
  cancel-in-progress: false

jobs:
  deploy:
    name: Deploy (default branch only)
    if: github.event_name == 'push' && github.ref == format('refs/heads/{0}', github.event.repository.default_branch)
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v4

      - name: Install nvoi
        run: |
          set -euo pipefail
          curl -fsSL -o nvoi "https://cdn.nvoi.to/bin/%s/linux-amd64/nvoi"
          chmod +x nvoi
          sudo mv nvoi /usr/local/bin/nvoi
          nvoi --version

      - name: nvoi deploy
        env:
%s        run: nvoi deploy --ci
`, version, envBlock.String())

	return workflowPath, []byte(content), nil
}
