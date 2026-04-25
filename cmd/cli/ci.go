package main

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
)

// newCICmd wires `nvoi ci <sub>`. Today one sub — `init`. Later subs
// (rotate, status, remove) hang off the same parent and share the
// CIProvider resolution path.
func newCICmd(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Manage CI/CD onboarding (GitHub Actions today)",
	}
	cmd.AddCommand(newCIInitCmd(rt))
	return cmd
}

// newCIInitCmd wires `nvoi ci init`: port every credential into the CI
// provider's secret store, commit the deploy workflow, and — when the
// default branch accepts direct pushes — hand back a commit URL; a PR
// URL otherwise. Idempotent: re-running overwrites secrets, updates
// the workflow file in place, and reuses an existing PR.
func newCIInitCmd(rt *runtime) *cobra.Command {
	var nvoiVersion string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Port credentials + commit deploy workflow to the CI provider",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := reconcile.ValidateConfig(rt.cfg); err != nil {
				return err
			}
			return runCIInit(ctx, rt, nvoiVersion)
		},
	}
	cmd.Flags().StringVar(&nvoiVersion, "nvoi-version", "",
		"pinned nvoi binary version the workflow downloads on the runner (recommended; empty = latest)")
	return cmd
}

// runCIInit is the full ci-init flow. Extracted from the Cobra RunE so
// the unit test can drive it with a pre-built runtime + faked git origin.
func runCIInit(ctx context.Context, rt *runtime, nvoiVersion string) error {
	ciName := rt.cfg.Providers.Ci
	if ciName == "" {
		return fmt.Errorf("providers.ci is not set in nvoi.yaml — add `providers.ci: github` and re-run")
	}

	// Resolve CI credentials. `repo` is inferable from `git remote
	// get-url origin` when the operator hasn't set GITHUB_REPO explicitly.
	// We do the inference here at the cmd/ boundary (not inside the
	// provider) because it's git-CLI-bound and cmd/ is the only place
	// that shells out.
	ciCreds, err := resolveProviderCreds(rt.dc.Creds, "ci", ciName)
	if err != nil {
		return fmt.Errorf("providers.ci: %w", err)
	}
	if ciCreds == nil {
		ciCreds = map[string]string{}
	}
	if ciCreds["repo"] == "" {
		if origin := gitOrigin(ctx); origin != "" {
			ciCreds["repo"] = origin
		}
	}

	ciProv, err := provider.ResolveCI(ciName, ciCreds)
	if err != nil {
		return err
	}
	defer ciProv.Close()

	if err := ciProv.ValidateCredentials(ctx); err != nil {
		return err
	}

	target := ciProv.Target()
	rt.out.Info(fmt.Sprintf("ci init → %s (%s/%s)", target.URL, target.Owner, target.Repo))

	// Collect the secret map — everything the deploy runtime will need
	// on the CI runner. Resolution rules:
	//   - Each declared provider's schema fields come from the active
	//     CredentialSource (env by default, backend when providers.secrets
	//     is set).
	//   - When providers.secrets is set, the backend's OWN bootstrap
	//     env vars are included directly from env (not from itself) so
	//     the runner can authenticate to the backend.
	//   - SSH_PRIVATE_KEY resolves through the same source, with the
	//     same tilde / id_* fallback as reconcile.Deploy uses today.
	//   - cfg.Secrets + service/cron refs + registry $VAR refs all
	//     resolve through the active source.
	secrets, err := collectCISecrets(rt.cfg, rt.dc.Creds)
	if err != nil {
		return err
	}

	rt.out.Info(fmt.Sprintf("syncing %s", utils.Pluralize(len(secrets), "secret", "")))
	if err := ciProv.SyncSecrets(ctx, secrets); err != nil {
		return err
	}

	// Sorted env list so the rendered workflow is byte-identical across
	// re-runs unless the set changes — the Contents API diff stays quiet,
	// repeated `ci init` runs don't create noise commits.
	path, content, err := ciProv.RenderWorkflow(provider.CIWorkflowPlan{
		NvoiVersion: nvoiVersion,
		SecretEnv:   utils.SortedKeys(secrets),
	})
	if err != nil {
		return err
	}

	url, err := ciProv.CommitFiles(ctx,
		[]provider.CIFile{{Path: path, Content: content}},
		"chore: nvoi ci init")
	if err != nil {
		return err
	}
	rt.out.Info(fmt.Sprintf("workflow committed → %s", url))
	return nil
}

// collectCISecrets walks the config and returns the exact env → value
// map the CI runner will need to execute `nvoi deploy`. The CLI is the
// right place for this (not pkg/provider/...) because it's the sole
// assembly point of every source: the active CredentialSource, the
// backend bootstrap env, and the operator's cwd for SSH key fallback.
//
// Empty values are dropped rather than shipped as empty secrets — empty
// on the runner means "env var absent", which the deploy engine would
// misread. The corresponding `cfg.Secrets` entry would then fail at
// ValidateConfig time on the runner, same as on the laptop; fail-fast
// is preserved.
func collectCISecrets(cfg *config.AppConfig, source provider.CredentialSource) (map[string]string, error) {
	out := map[string]string{}

	add := func(name string) {
		if name == "" {
			return
		}
		v, _ := source.Get(name)
		if v == "" {
			return
		}
		out[name] = v
	}

	// 1. Every declared provider's schema fields.
	providerPairs := []struct{ kind, name string }{
		{"infra", cfg.Providers.Infra},
		{"dns", cfg.Providers.DNS},
		{"storage", cfg.Providers.Storage},
		{"build", cfg.Providers.Build},
		{"tunnel", cfg.Providers.Tunnel},
	}
	for _, p := range providerPairs {
		if p.name == "" {
			continue
		}
		schema, err := provider.GetSchema(p.kind, p.name)
		if err != nil {
			// Typo — validator catches this earlier in happy-path
			// invocations, but we check here too so a raw
			// provider.ResolveCI caller still gets a clear error.
			return nil, fmt.Errorf("providers.%s: %w", p.kind, err)
		}
		for _, f := range schema.Fields {
			add(f.EnvVar)
		}
	}

	// 2. Secrets backend bootstrap creds. These ALWAYS come straight
	// from env (not via the active source) — the backend can't
	// authenticate to itself, so its own bootstrap creds are the one
	// field that stays env-native even in strict mode.
	if sp := cfg.Providers.Secrets; sp != nil && sp.Kind != "" {
		schema, err := provider.GetSchema("secrets", sp.Kind)
		if err != nil {
			return nil, fmt.Errorf("providers.secrets: %w", err)
		}
		envSrc := provider.EnvSource{}
		for _, f := range schema.Fields {
			if v, _ := envSrc.Get(f.EnvVar); v != "" {
				out[f.EnvVar] = v
			}
		}
	}

	// 3. SSH private key. Reuse resolveSSHKey so laptop and CI pick up
	// the same bytes — tilde expansion, id_ed25519 / id_rsa fallback,
	// strict-mode disk rejection when providers.secrets is set.
	if pem, err := resolveSSHKey(source); err == nil && len(pem) > 0 {
		out["SSH_PRIVATE_KEY"] = string(pem)
	}
	// SSH_PRIVATE_KEY is load-bearing: without it, the runner can't
	// dial the master. No silent success path here — the runner would
	// fail obscurely on first connect. If the operator intentionally
	// runs keyless (e.g. backend-stored-only), they already have
	// SSH_PRIVATE_KEY in the backend; the resolve above picks it up.
	if out["SSH_PRIVATE_KEY"] == "" {
		return nil, fmt.Errorf("SSH_PRIVATE_KEY not found — set SSH_PRIVATE_KEY in env or the secrets backend, or place ~/.ssh/id_ed25519")
	}

	// 4. Top-level cfg.Secrets — every name the reconciler will try to
	// resolve at deploy time.
	for _, name := range cfg.Secrets {
		add(name)
	}

	// 5. Service/cron `secrets:` refs — two forms:
	//    bare `FOO`            → runner needs env var FOO
	//    `ALIAS=$BAR`          → runner needs env var BAR; the service
	//                             sees it renamed to ALIAS at pod time
	// Port the backing env var for each form. `cfg.ServiceSecrets()`
	// only returns the left side of `=`, which for `ALIAS=$BAR` is
	// ALIAS — the WRONG key to port. Walk the raw refs.
	portRef := func(ref string) {
		envKey, rhs := kube.ParseSecretRef(ref)
		if envKey == rhs {
			// bare `FOO`.
			add(envKey)
			return
		}
		// KEY=$VAR form — port every `$VAR` on the right.
		for _, v := range utils.ExtractVarRefs(rhs) {
			add(v)
		}
	}
	for _, svc := range cfg.Services {
		for _, ref := range svc.Secrets {
			portRef(ref)
		}
	}
	for _, cron := range cfg.Crons {
		for _, ref := range cron.Secrets {
			portRef(ref)
		}
	}

	// 6. Registry $VAR refs in username / password. Port the backing
	// env vars (GITHUB_TOKEN, …), not the literal "$VAR" strings.
	for _, def := range cfg.Registry {
		for _, raw := range []string{def.Username, def.Password} {
			for _, v := range utils.ExtractVarRefs(raw) {
				add(v)
			}
		}
	}

	return out, nil
}
