// Package utils is the single source of truth for all resource names.
//
// Every resource nvoi creates is named deterministically from NVOI_ENV.
// Same env → same names → same resources. No UUIDs, no state files.
// Idempotency through naming: always call GetXByName(names.X(...)).
// If found → reuse. If not → create.
package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// ── Names ────────────────────────────────────────────────────────────────────────

// Names provides all resource names for an app + environment.
// NVOI_APP_NAME=dummy-rails NVOI_ENV=production → nvoi-dummy-rails-production-*.
// Different app or env = brand new infrastructure.
type Names struct {
	app string
	env string
}

// NewNames creates Names from app + env strings.
// Caller (cmd layer) is responsible for reading env vars.
func NewNames(app, env string) (*Names, error) {
	if app == "" || env == "" {
		return nil, fmt.Errorf("app and env are required")
	}
	return &Names{app: sanitize(app), env: sanitize(env)}, nil
}

func (n *Names) App() string { return n.app }
func (n *Names) Env() string { return n.env }

// ── Infrastructure names ───────────────────────────────────────────────────────

func (n *Names) Base() string              { return fmt.Sprintf("nvoi-%s-%s", n.app, n.env) }
func (n *Names) Firewall() string          { return n.Base() + "-fw" }
func (n *Names) Network() string           { return n.Base() + "-net" }
func (n *Names) Server(key string) string  { return fmt.Sprintf("%s-%s", n.Base(), key) }
func (n *Names) Stack() string             { return n.Base() }
func (n *Names) Volume(name string) string { return fmt.Sprintf("%s-%s", n.Base(), name) }
func (n *Names) Bucket(name string) string { return fmt.Sprintf("%s-%s", n.Base(), name) }

// ── K8s ────────────────────────────────────────────────────────────────────────

// KubeNamespace — each app+env gets its own. Service names stay short.
// POSTGRES_HOST=db just works. No rewriting magic.
func (n *Names) KubeNamespace() string          { return n.Base() }
func (n *Names) KubeWorkload(svc string) string { return svc }
func (n *Names) KubeService(svc string) string  { return svc }
func (n *Names) KubeCaddy() string              { return "caddy" }
func (n *Names) KubeCaddyConfig() string        { return "caddy-config" }
func (n *Names) KubeSecrets() string            { return "secrets" }

// ── Labels ─────────────────────────────────────────────────────────────────────

func (n *Names) Labels() map[string]string {
	return map[string]string{
		"managed-by": "nvoi",
		"app":        n.Base(),
		"env":        n.env,
	}
}

// ── Remote paths ───────────────────────────────────────────────────────────────

func (n *Names) VolumeMountPath(name string) string {
	return fmt.Sprintf("/mnt/data/%s-%s", n.Base(), name)
}

func (n *Names) CaddyDataPath() string {
	return fmt.Sprintf("/var/lib/nvoi/caddy/%s", n.Stack())
}

func (n *Names) CaddyCertPath(domain string) string {
	return fmt.Sprintf("%s/caddy/certificates/acme-v02.api.letsencrypt.org-directory/%s/%s.json",
		n.CaddyDataPath(), domain, domain)
}

func (n *Names) NamedVolumeHostPath(volume string) string {
	return fmt.Sprintf("/var/lib/nvoi/volumes/%s/%s", n.Stack(), volume)
}

// ── K3s paths ──────────────────────────────────────────────────────────────────

const (
	KubeconfigPath      = "/etc/rancher/k3s/k3s.yaml"
	K3sTokenPath        = "/var/lib/rancher/k3s/server/node-token"
	K3sConfigDir        = "/etc/rancher/k3s"
	K3sRegistriesConfig = "/etc/rancher/k3s/registries.yaml"
)

// ── Docker ─────────────────────────────────────────────────────────────────────

const (
	CaddyImage         = "caddy:2-alpine"
	RegistryImage      = "registry:2"
	RegistryPort       = 5000
	DockerDaemonConfig = "/etc/docker/daemon.json"
)

func RegistryAddr(ip string) string {
	return fmt.Sprintf("%s:%d", ip, RegistryPort)
}

// ── Remote file paths ──────────────────────────────────────────────────────────

func KubeManifestPath() string     { return fmt.Sprintf("/home/%s/nvoi-k8s.yaml", DefaultUser) }
func EnvFilePath() string          { return fmt.Sprintf("/home/%s/.nvoi.env", DefaultUser) }
func DeployKeyPath() string        { return fmt.Sprintf("/home/%s/.ssh/nvoi_deploy_key", DefaultUser) }
func CaddyfileStagingPath() string { return fmt.Sprintf("/home/%s/Caddyfile.k8s", DefaultUser) }

// ── K8s label keys ─────────────────────────────────────────────────────────────

const (
	LabelAppName        = "app.kubernetes.io/name"
	LabelAppManagedBy   = "app.kubernetes.io/managed-by"
	LabelManagedBy      = "nvoi"
	LabelNvoiService    = "nvoi/service"
	LabelNvoiStack      = "nvoi/stack"
	LabelNvoiRole       = "nvoi-role"
	LabelConfigChecksum = "nvoi/config-checksum"
	RoleMaster          = "master"
)

// ── Storage env naming ─────────────────────────────────────────────────────────

func StorageEnvPrefix(bucketName string) string {
	upper := strings.ToUpper(bucketName)
	return "STORAGE_" + strings.ReplaceAll(upper, "-", "_")
}

// ── Network CIDRs ──────────────────────────────────────────────────────────────

const (
	PrivateNetworkCIDR   = "10.0.0.0/16"
	PrivateNetworkSubnet = "10.0.1.0/24"
	K3sClusterCIDR       = "10.42.0.0/16"
	K3sServiceCIDR       = "10.43.0.0/16"
)

// ── Constants ──────────────────────────────────────────────────────────────────

const (
	DefaultUser  = "deploy"
	DefaultImage = "ubuntu-24.04"
	MasterKey    = "master"
)

// ── Volume parsing ─────────────────────────────────────────────────────────────

// ParseVolumeMount splits "pgdata:/var/lib/postgresql/data" → ("pgdata", "/var/lib/...", true, true).
func ParseVolumeMount(s string) (source, target string, named bool, ok bool) {
	i := strings.Index(s, ":")
	if i < 0 {
		return "", "", false, false
	}
	source = s[:i]
	rest := s[i+1:]
	if j := strings.Index(rest, ":"); j >= 0 {
		target = rest[:j]
	} else {
		target = rest
	}
	if source == "" {
		return "", "", false, false
	}
	named = source[0] != '/' && source[0] != '.'
	return source, target, named, true
}

// ── Internal ───────────────────────────────────────────────────────────────────

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

func sanitize(s string) string {
	s = strings.ToLower(s)
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}
