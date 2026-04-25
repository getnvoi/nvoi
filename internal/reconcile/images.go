package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// BuildImages walks cfg.Services in sorted order and, for each service with
// a non-empty `build:` directive, resolves the configured BuildProvider
// (local / ssh / daytona) and calls bp.Build(req). The imageRef the provider
// returns IS the one stamped on the PodSpec — normally identical to the
// requested tag, but a content-addressed provider could return a digest ref.
//
// Named BuildImages (not Build) to free the word "build" for the outer
// BuildProvider family in pkg/provider/build.go — "build" there is the
// substrate choice (local vs ssh vs daytona); this function is the
// orchestration step that dispatches one Build() per service.
//
// platform ("linux/amd64" or "linux/arm64") is derived by the caller from
// infra.ArchForType(masterServerType). Stamped on every BuildRequest so
// the image arch always matches the target server.
//
// builders is the SSH-addressable list of role: builder servers as reported
// by infra.BuilderTargets. Populated only when the selected build provider
// sets RequiresBuilders=true (reconcile.Deploy calls ProvisionBuilders +
// BuilderTargets before this step); nil otherwise. ssh consumes it; local
// and daytona ignore it.
//
// Runs pre-Bootstrap — a build failure must not leave half-provisioned
// servers behind. Services without build: set are skipped (they pull
// their image: tag verbatim).
func BuildImages(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, platform string, builders []provider.BuilderTarget) error {
	// Fast exit: no service has a build: directive. Skip the whole pass.
	anyBuild := false
	for _, svc := range cfg.Services {
		if svc.Build != nil && svc.Build.Context != "" {
			anyBuild = true
			break
		}
	}
	if !anyBuild {
		return nil
	}

	// Platform must be known. An empty platform means the caller failed to
	// derive the server arch — proceeding would silently build for the host
	// arch, producing an image that fails to run on a cross-arch target.
	if platform == "" {
		return fmt.Errorf("build: target platform unknown — cannot determine server architecture")
	}

	// Resolve the BuildProvider once. Unset → "local" (matches validator).
	buildName := cfg.Providers.Build
	if buildName == "" {
		buildName = "local"
	}
	buildCreds, err := resolveBuildCreds(dc, buildName)
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}
	bp, err := provider.ResolveBuild(buildName, buildCreds)
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}
	defer func() { _ = bp.Close() }()

	// Resolve registry creds once for the whole pass. Missing env var errors
	// surface here, before any BuildProvider touches the network.
	regCreds, err := resolveRegistries(dc, cfg)
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}

	// Deterministic order — same service always builds first across re-runs,
	// so the deploy log diff is stable.
	for _, name := range utils.SortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		if svc.Build == nil || svc.Build.Context == "" {
			continue
		}

		// Full ref including host + user-tag-"-"-hash (or just hash). Same
		// function Services() uses when writing the PodSpec, so the registry
		// and the cluster always agree on the image string.
		fullImage, err := ResolveImage(cfg, name, dc.Cluster.DeployHash)
		if err != nil {
			return err
		}
		host := imageRegistryHost(fullImage)
		auth, ok := regCreds[host]
		if !ok {
			// Validation already catches this — belt-and-suspenders in case
			// someone edits config between validate and build.
			return fmt.Errorf("services.%s.build: no credentials for registry %q", name, host)
		}

		req := provider.BuildRequest{
			Service:    name,
			Context:    svc.Build.Context,
			Dockerfile: svc.Build.Dockerfile,
			Platform:   platform,
			Image:      fullImage,
			Registry: provider.RegistryAuth{
				Host:     host,
				Username: auth.Username,
				Password: auth.Password,
			},
			Builders:  builders,
			SSHKey:    dc.Cluster.SSHKey,
			GitRemote: dc.GitRemote,
			GitRef:    dc.GitRef,
			Output:    dc.Cluster.Log().Writer(),
		}

		ref, err := bp.Build(ctx, req)
		if err != nil {
			return fmt.Errorf("services.%s.build: %w", name, err)
		}
		if ref == "" {
			return fmt.Errorf("services.%s.build: provider %q returned empty image ref", name, buildName)
		}
		// Future: if ref != fullImage (content-addressed builder), stash on
		// DeployContext so Services() stamps the returned ref on the PodSpec.
		// Today every registered provider returns req.Image verbatim, so we
		// just validate non-empty and move on.
	}

	return nil
}

// resolveBuildCreds walks the BuildProvider's credential schema, pulling
// each declared env var out of dc.Creds. The local provider has an empty
// schema so this returns {}; ssh has no creds either (uses dc.Cluster.SSHKey
// carried via BuildRequest); daytona requires DAYTONA_API_KEY.
func resolveBuildCreds(dc *config.DeployContext, name string) (map[string]string, error) {
	schema, err := provider.GetBuildSchema(name)
	if err != nil {
		return nil, err
	}
	source := dc.Creds
	if source == nil {
		source = provider.MapSource{M: map[string]string{}}
	}
	return provider.ResolveFrom(schema, source)
}
