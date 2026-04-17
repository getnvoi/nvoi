package reconcile

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ValidateConfig catches all misconfigurations before touching infra.
// Returns the first error found — fail fast, fix one thing at a time.
func ValidateConfig(cfg *config.AppConfig) error {
	// ── Identity ──────────────────────────────────────────────────────────
	if cfg.App == "" {
		return fmt.Errorf("app is required")
	}
	if err := utils.ValidateName("app", cfg.App); err != nil {
		return err
	}
	if cfg.Env == "" {
		return fmt.Errorf("env is required")
	}
	if err := utils.ValidateName("env", cfg.Env); err != nil {
		return err
	}

	// ── Providers ─────────────────────────────────────────────────────────
	if cfg.Providers.Compute == "" {
		return fmt.Errorf("providers.compute is required")
	}

	// ── Servers ───────────────────────────────────────────────────────────
	if len(cfg.Servers) == 0 {
		return fmt.Errorf("at least one server is required")
	}
	masterCount := 0
	for name, srv := range cfg.Servers {
		if err := utils.ValidateName("servers."+name, name); err != nil {
			return err
		}
		if srv.Type == "" {
			return fmt.Errorf("servers.%s.type is required", name)
		}
		if srv.Region == "" {
			return fmt.Errorf("servers.%s.region is required", name)
		}
		if srv.Role == "" {
			return fmt.Errorf("servers.%s.role is required (master or worker)", name)
		}
		if srv.Role != "master" && srv.Role != "worker" {
			return fmt.Errorf("servers.%s.role must be master or worker, got %q", name, srv.Role)
		}
		if srv.Disk < 0 {
			return fmt.Errorf("servers.%s.disk must be >= 0", name)
		}
		if srv.Disk > 0 && cfg.Providers.Compute == "hetzner" {
			return fmt.Errorf("servers.%s.disk: hetzner does not support custom root disk sizes — disk is fixed per server type", name)
		}
		if srv.Role == "master" {
			masterCount++
		}
	}
	if masterCount == 0 {
		return fmt.Errorf("servers: exactly one server must have role: master")
	}
	if masterCount > 1 {
		return fmt.Errorf("servers: only one master is supported")
	}

	// ── Volumes ───────────────────────────────────────────────────────────
	for name, vol := range cfg.Volumes {
		if err := utils.ValidateName("volumes."+name, name); err != nil {
			return err
		}
		if vol.Size <= 0 {
			return fmt.Errorf("volumes.%s.size must be > 0", name)
		}
		if vol.Server == "" {
			return fmt.Errorf("volumes.%s.server is required", name)
		}
		if _, ok := cfg.Servers[vol.Server]; !ok {
			return fmt.Errorf("volumes.%s.server: %q is not a defined server", name, vol.Server)
		}
	}

	// ── Storage ──────────────────────────────────────────────────────────
	for name := range cfg.Storage {
		if err := utils.ValidateName("storage."+name, name); err != nil {
			return err
		}
	}

	// ── Services ──────────────────────────────────────────────────────────
	for name, svc := range cfg.Services {
		if err := utils.ValidateName("services."+name, name); err != nil {
			return err
		}
		if svc.Image == "" {
			return fmt.Errorf("services.%s: image is required", name)
		}
		// build: requires a registry to push to AND auth in cfg.Registry.
		// Image references may be repo-only (host inferred from a single
		// registry entry, Kamal-style) or fully qualified (host explicit).
		// Ambiguity — bare repo with multiple registries declared — is a
		// hard error at validate so the operator never gets surprised
		// mid-deploy.
		if svc.Build != nil && svc.Build.Context != "" {
			if len(cfg.Registry) == 0 {
				return fmt.Errorf("services.%s.build: set but `registry:` block is missing — nvoi needs push credentials", name)
			}
			if host := imageRegistryHost(svc.Image); host != "" {
				// Explicit host — must match a declared registry.
				if _, ok := cfg.Registry[host]; !ok {
					return fmt.Errorf("services.%s.build: image targets registry %q but no `registry.%s` entry declares credentials", name, host, host)
				}
			} else {
				// No host — infer from the single registry if exactly one
				// is declared; reject ambiguity otherwise.
				if svc.Image == "" {
					return fmt.Errorf("services.%s.build: image is required", name)
				}
				if strings.Contains(svc.Image, "/") == false {
					// Bare shortname like `nginx` — no repo namespace. Push
					// would land at `<host>/nginx:<hash>` which is almost
					// certainly not what the user wants.
					return fmt.Errorf("services.%s.build: image %q has no repo namespace — use `<org>/<name>` (e.g. `deemx/nvoi-api`) or a fully qualified tag", name, svc.Image)
				}
				if len(cfg.Registry) > 1 {
					return fmt.Errorf("services.%s.image: %q has no host prefix but multiple registries are declared — write a fully qualified tag (e.g. `ghcr.io/%s`) to disambiguate", name, svc.Image, svc.Image)
				}
			}
		}
		if svc.Server != "" && len(svc.Servers) > 0 {
			return fmt.Errorf("services.%s: server and servers are mutually exclusive", name)
		}
		for _, s := range svc.ResolvedServers() {
			if _, ok := cfg.Servers[s]; !ok {
				return fmt.Errorf("services.%s.server: %q is not a defined server", name, s)
			}
		}
		if svc.Replicas < 0 {
			return fmt.Errorf("services.%s.replicas must be >= 0", name)
		}
		for _, ref := range svc.Storage {
			if _, ok := cfg.Storage[ref]; !ok {
				return fmt.Errorf("services.%s.storage: %q is not a defined storage", name, ref)
			}
		}
		if err := validateVolumeMounts(cfg, "services."+name, svc.Volumes); err != nil {
			return err
		}
		if len(svc.Servers) > 1 && hasNamedVolume(cfg, svc.Volumes) {
			return fmt.Errorf("services.%s: multiple servers with a volume mount — volumes are pinned to one server", name)
		}
		if err := validateVolumeServer(cfg, name, svc.Server, svc.Volumes); err != nil {
			return err
		}
		if err := validateSecretRefs("services."+name+".secrets", svc.Secrets); err != nil {
			return err
		}
		for _, dep := range svc.DependsOn {
			if dep == name {
				return fmt.Errorf("services.%s.depends_on: cannot depend on itself", name)
			}
			if _, ok := cfg.Services[dep]; !ok {
				return fmt.Errorf("services.%s.depends_on: %q is not a defined service", name, dep)
			}
		}
	}

	// Detect dependency cycles across all services.
	if cyc := findDependencyCycle(cfg.Services); cyc != "" {
		return fmt.Errorf("services: depends_on cycle detected: %s", cyc)
	}

	// ── Crons ─────────────────────────────────────────────────────────────
	for name, cron := range cfg.Crons {
		if err := utils.ValidateName("crons."+name, name); err != nil {
			return err
		}
		if cron.Image == "" {
			return fmt.Errorf("crons.%s: image is required", name)
		}
		if cron.Schedule == "" {
			return fmt.Errorf("crons.%s.schedule is required", name)
		}
		if cron.Server != "" && len(cron.Servers) > 0 {
			return fmt.Errorf("crons.%s: server and servers are mutually exclusive", name)
		}
		for _, s := range cron.ResolvedServers() {
			if _, ok := cfg.Servers[s]; !ok {
				return fmt.Errorf("crons.%s.server: %q is not a defined server", name, s)
			}
		}
		for _, ref := range cron.Storage {
			if _, ok := cfg.Storage[ref]; !ok {
				return fmt.Errorf("crons.%s.storage: %q is not a defined storage", name, ref)
			}
		}
		if err := validateVolumeMounts(cfg, "crons."+name, cron.Volumes); err != nil {
			return err
		}
		if len(cron.Servers) > 1 && hasNamedVolume(cfg, cron.Volumes) {
			return fmt.Errorf("crons.%s: multiple servers with a volume mount — volumes are pinned to one server", name)
		}
		if err := validateVolumeServer(cfg, name, cron.Server, cron.Volumes); err != nil {
			return err
		}
		if err := validateSecretRefs("crons."+name+".secrets", cron.Secrets); err != nil {
			return err
		}
	}

	// ── Domains ───────────────────────────────────────────────────────────
	for svcName, domains := range cfg.Domains {
		svc, ok := cfg.Services[svcName]
		if !ok {
			return fmt.Errorf("domains.%s: %q is not a defined service", svcName, svcName)
		}
		if svc.Port == 0 {
			return fmt.Errorf("domains.%s: service %q has no port", svcName, svcName)
		}
		if len(domains) == 0 {
			return fmt.Errorf("domains.%s: at least one domain is required", svcName)
		}
		for _, d := range domains {
			if err := utils.ValidateDomain("domains."+svcName, d); err != nil {
				return err
			}
		}
		if svc.Replicas == 1 {
			return fmt.Errorf("services.%s: web-facing services require replicas >= 2 for zero-downtime rolling updates (omit replicas to default to 2)", svcName)
		}
	}

	// ── Secrets ───────────────────────────────────────────────────────────
	for _, name := range cfg.Secrets {
		if err := utils.ValidateEnvVarName("secrets."+name, name); err != nil {
			return err
		}
	}

	// ── Registry ──────────────────────────────────────────────────────────
	// Each key is a registry hostname (docker.io, ghcr.io, registry.example.com).
	// username + password are required and may be `$VAR` references — value
	// resolution and the missing-env-var error happens at deploy time, not here.
	for host, def := range cfg.Registry {
		if host == "" {
			return fmt.Errorf("registry: empty hostname key")
		}
		if !validRegistryHost(host) {
			return fmt.Errorf("registry.%s: invalid hostname (expected DNS host like docker.io, ghcr.io, or registry.example.com:5000)", host)
		}
		if strings.TrimSpace(def.Username) == "" {
			return fmt.Errorf("registry.%s.username is required", host)
		}
		if strings.TrimSpace(def.Password) == "" {
			return fmt.Errorf("registry.%s.password is required", host)
		}
	}

	return nil
}

// validRegistryHost accepts DNS-style hostnames optionally followed by :port.
// Examples that pass: docker.io, ghcr.io, gcr.io, registry.example.com,
// 10.0.1.1:5000, registry.local:443.
//
// Looser than ValidateDomain because container registries commonly use
// single-label hosts (docker.io was historically index.docker.io but the
// short form is canonical now) and bare IP:port endpoints.
func validRegistryHost(host string) bool {
	if host == "" {
		return false
	}
	// Strip optional :port.
	if i := strings.LastIndex(host, ":"); i >= 0 {
		port := host[i+1:]
		if port == "" {
			return false
		}
		for _, c := range port {
			if c < '0' || c > '9' {
				return false
			}
		}
		host = host[:i]
	}
	if host == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return false
		}
		for i, c := range label {
			isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
			isDigit := c >= '0' && c <= '9'
			isHyphen := c == '-'
			if !(isAlpha || isDigit || isHyphen) {
				return false
			}
			if isHyphen && (i == 0 || i == len(label)-1) {
				return false
			}
		}
	}
	return true
}

func hasNamedVolume(cfg *config.AppConfig, mounts []string) bool {
	for _, mount := range mounts {
		source, _, ok := strings.Cut(mount, ":")
		if !ok {
			continue
		}
		if !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") {
			if _, ok := cfg.Volumes[source]; ok {
				return true
			}
		}
	}
	return false
}

func validateVolumeMounts(cfg *config.AppConfig, context string, mounts []string) error {
	for _, mount := range mounts {
		source, _, ok := strings.Cut(mount, ":")
		if !ok {
			return fmt.Errorf("%s.volumes: %q must be name:/path", context, mount)
		}
		if !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") {
			if _, ok := cfg.Volumes[source]; !ok {
				return fmt.Errorf("%s.volumes: volume %q is not defined", context, source)
			}
		}
	}
	return nil
}

func validateVolumeServer(cfg *config.AppConfig, workload, server string, mounts []string) error {
	for _, mount := range mounts {
		volName, _, ok := strings.Cut(mount, ":")
		if !ok || strings.HasPrefix(volName, "/") || strings.HasPrefix(volName, ".") {
			continue
		}
		vol, exists := cfg.Volumes[volName]
		if !exists {
			continue
		}
		if server != "" && server != vol.Server {
			return fmt.Errorf("%s: mounts volume %q (on server %q) but has server: %q — cannot move",
				workload, volName, vol.Server, server)
		}
	}
	return nil
}

// imageRegistryHost extracts the registry host from an image reference.
//
//	"ghcr.io/org/app:v1"        → "ghcr.io"
//	"registry.example.com:5000/foo:v1" → "registry.example.com:5000"
//	"docker.io/library/nginx"   → "docker.io"
//	"nginx"                     → ""  (bare shortname — caller treats as invalid for build targets)
//	"alpine:3.19"               → ""  (bare shortname with tag — same)
//
// The heuristic: everything before the first `/` is the host iff it
// contains a `.` or `:` or equals "localhost". Otherwise there's no host.
func imageRegistryHost(image string) string {
	slash := strings.IndexByte(image, '/')
	if slash <= 0 {
		return ""
	}
	first := image[:slash]
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return first
	}
	return ""
}

func validateSecretRefs(context string, refs []string) error {
	for _, ref := range refs {
		envName, secretKey := kube.ParseSecretRef(ref)
		if err := utils.ValidateEnvVarName(context, envName); err != nil {
			return err
		}
		// If = is present, the right side MUST contain $.
		// "FOO=BAR" without $ is ambiguous — reject it.
		if strings.Contains(ref, "=") && !hasVarRef(secretKey) {
			return fmt.Errorf("%s: %q requires $ on the right side of = (e.g. %s=$%s)",
				context, ref, envName, secretKey)
		}
	}
	return nil
}
