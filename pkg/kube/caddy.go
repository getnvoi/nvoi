package kube

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// IngressRoute maps a service to its public domains.
type IngressRoute struct {
	Service      string
	Port         int
	Domains      []string
	UseTLSSecret bool // Render this site with mounted TLS material instead of ACME
}

// CaddyTLSSecretName is defined in pkg/utils/naming.go.
var CaddyTLSSecretName = utils.CaddyTLSSecretName

// ApplyCaddyConfig updates the ConfigMap and hot-reloads Caddy.
// If Caddy isn't running yet, deploys it. Zero downtime for config changes.
// Mounts the shared TLS secret when any route uses secret-backed TLS material.
func ApplyCaddyConfig(ctx context.Context, ssh utils.SSHClient, ns string, routes []IngressRoute, names *utils.Names) error {
	if len(routes) == 0 {
		return nil
	}

	hasTLSSecret := false
	for _, r := range routes {
		if r.UseTLSSecret {
			hasTLSSecret = true
			break
		}
	}

	caddyfile := generateCaddyfile(routes, ns)

	// Apply ConfigMap
	cmYAML, err := generateConfigMapYAML(caddyfile, names, ns)
	if err != nil {
		return fmt.Errorf("generate configmap: %w", err)
	}
	if err := Apply(ctx, ssh, ns, cmYAML); err != nil {
		return fmt.Errorf("apply configmap: %w", err)
	}

	// Check if Caddy deployment exists
	existingDep, getErr := ssh.Run(ctx, kubectl(ns, fmt.Sprintf("get deployment %s -o jsonpath='{.spec.template.spec.volumes[*].name}' 2>/dev/null", names.KubeCaddy())))
	deploymentExists := getErr == nil

	if !deploymentExists {
		// First deploy — create the Deployment
		depYAML, err := generateDeploymentYAML(names, ns, hasTLSSecret)
		if err != nil {
			return fmt.Errorf("generate deployment: %w", err)
		}
		if err := Apply(ctx, ssh, ns, depYAML); err != nil {
			return fmt.Errorf("apply deployment: %w", err)
		}
		return nil // new pod will pick up the ConfigMap on start
	}

	// Re-apply deployment if TLS secret usage changed (volume mounts differ).
	existingHasTLS := strings.Contains(string(existingDep), "tls")
	if hasTLSSecret != existingHasTLS {
		depYAML, err := generateDeploymentYAML(names, ns, hasTLSSecret)
		if err != nil {
			return fmt.Errorf("generate deployment: %w", err)
		}
		if err := Apply(ctx, ssh, ns, depYAML); err != nil {
			return fmt.Errorf("apply deployment: %w", err)
		}
		return nil // new pod will pick up both the ConfigMap and new volume mounts
	}

	// Caddy already running, same mode — hot reload.
	// ConfigMap volume sync takes up to kubelet sync period (default 60s).
	// Poll until the file hash matches, then reload.
	podName, err := caddyPodName(ctx, ssh, ns, names.KubeCaddy())
	if err != nil {
		return fmt.Errorf("find caddy pod: %w", err)
	}

	expectedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(caddyfile)))
	deadline := time.After(CaddyReloadDelay)
	for {
		out, hashErr := ssh.Run(ctx, kubectl(ns, fmt.Sprintf(
			"exec %s -- sha256sum /etc/caddy/Caddyfile", podName)))
		if hashErr == nil {
			fields := strings.Fields(strings.TrimSpace(string(out)))
			if len(fields) > 0 && fields[0] == expectedHash {
				break
			}
		}
		select {
		case <-deadline:
			// Timeout — proceed with reload anyway
			goto reload
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
reload:

	_, err = ssh.Run(ctx, kubectl(ns, fmt.Sprintf(
		"exec %s -- caddy reload --config /etc/caddy/Caddyfile --force", podName)))
	if err != nil {
		return fmt.Errorf("caddy reload: %w", err)
	}

	return nil
}

// CaddyReloadDelay is the wait for kubelet to sync ConfigMap to the volume.
// Variable for testing.
var CaddyReloadDelay = 5 * time.Second

// caddyPodName finds the running Caddy pod name.
func caddyPodName(ctx context.Context, ssh utils.SSHClient, ns, deploymentName string) (string, error) {
	out, err := ssh.Run(ctx, kubectl(ns, fmt.Sprintf(
		"get pods -l %s=%s -o jsonpath='{.items[0].metadata.name}'", utils.LabelAppName, deploymentName)))
	if err != nil {
		return "", err
	}
	name := strings.Trim(strings.TrimSpace(string(out)), "'")
	if name == "" {
		return "", fmt.Errorf("no caddy pod found")
	}
	return name, nil
}

// UpsertTLSSecret creates or updates a k8s TLS secret with cert and key.
func UpsertTLSSecret(ctx context.Context, ssh utils.SSHClient, ns, name, cert, key string) error {
	secret := corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Type:       corev1.SecretTypeTLS,
		StringData: map[string]string{
			"tls.crt": cert,
			"tls.key": key,
		},
	}
	b, err := sigsyaml.Marshal(secret)
	if err != nil {
		return fmt.Errorf("marshal tls secret: %w", err)
	}
	return Apply(ctx, ssh, ns, strings.TrimSpace(string(b)))
}

// GenerateCaddyManifest produces the full Caddy ConfigMap + Deployment YAML.
// Used only for first deploy or when the deployment itself needs updating (image change).
func GenerateCaddyManifest(routes []IngressRoute, names *utils.Names) (string, error) {
	if len(routes) == 0 {
		return "", nil
	}

	hasTLSSecret := false
	for _, r := range routes {
		if r.UseTLSSecret {
			hasTLSSecret = true
			break
		}
	}

	ns := names.KubeNamespace()
	caddyfile := generateCaddyfile(routes, ns)

	cmYAML, err := generateConfigMapYAML(caddyfile, names, ns)
	if err != nil {
		return "", err
	}
	depYAML, err := generateDeploymentYAML(names, ns, hasTLSSecret)
	if err != nil {
		return "", err
	}

	return cmYAML + "\n---\n" + depYAML, nil
}

func generateConfigMapYAML(caddyfile string, names *utils.Names, ns string) (string, error) {
	cm := corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: names.KubeCaddyConfig(), Namespace: ns},
		Data:       map[string]string{"Caddyfile": caddyfile},
	}
	b, err := sigsyaml.Marshal(cm)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func generateDeploymentYAML(names *utils.Names, ns string, hasProxy bool) (string, error) {
	one := int32(1)
	hostPathType := corev1.HostPathDirectoryOrCreate
	caddyName := names.KubeCaddy()
	labels := map[string]string{
		utils.LabelAppName:      caddyName,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
	}

	dep := appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: caddyName, Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{utils.LabelAppName: caddyName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					HostNetwork: true,
					DNSPolicy:   corev1.DNSClusterFirstWithHostNet,
					NodeSelector: map[string]string{
						utils.LabelNvoiRole: utils.RoleMaster,
					},
					Containers: []corev1.Container{{
						Name:  caddyName,
						Image: utils.CaddyImage,
						Ports: []corev1.ContainerPort{
							{ContainerPort: 80, HostPort: 80},
							{ContainerPort: 443, HostPort: 443},
						},
						VolumeMounts: caddyVolumeMounts(hasProxy),
					}},
					Volumes: caddyVolumes(names, hostPathType, hasProxy),
				},
			},
		},
	}
	b, err := sigsyaml.Marshal(dep)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func caddyVolumeMounts(proxy bool) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{
		{Name: "config", MountPath: "/etc/caddy/Caddyfile", SubPath: "Caddyfile", ReadOnly: true},
		{Name: "data", MountPath: "/data"},
	}
	if proxy {
		mounts = append(mounts, corev1.VolumeMount{
			Name: "tls", MountPath: "/etc/caddy/tls", ReadOnly: true,
		})
	}
	return mounts
}

func caddyVolumes(names *utils.Names, hostPathType corev1.HostPathType, proxy bool) []corev1.Volume {
	vols := []corev1.Volume{
		{Name: "config", VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: names.KubeCaddyConfig()},
			},
		}},
		{Name: "data", VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: names.CaddyDataPath(),
				Type: &hostPathType,
			},
		}},
	}
	if proxy {
		vols = append(vols, corev1.Volume{
			Name: "tls", VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: CaddyTLSSecretName,
				},
			},
		})
	}
	return vols
}

// ── Caddyfile generation ──────────────────────────────────────────────────────

// generateCaddyfile produces a Caddyfile routing domains to k8s services.
func generateCaddyfile(routes []IngressRoute, namespace string) string {
	sorted := make([]IngressRoute, len(routes))
	copy(sorted, routes)
	sort.Slice(sorted, func(i, j int) bool {
		if len(sorted[i].Domains) == 0 {
			return true
		}
		if len(sorted[j].Domains) == 0 {
			return false
		}
		return sorted[i].Domains[0] < sorted[j].Domains[0]
	})

	var b strings.Builder

	var secretTLS []IngressRoute
	var direct []IngressRoute
	for _, route := range sorted {
		if route.UseTLSSecret {
			secretTLS = append(secretTLS, route)
		} else {
			direct = append(direct, route)
		}
	}

	for i, route := range direct {
		if i > 0 {
			b.WriteString("\n")
		}
		for j, domain := range route.Domains {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(domain)
		}
		b.WriteString(" {\n")
		fmt.Fprintf(&b, "\treverse_proxy %s.%s.svc.cluster.local:%d\n", route.Service, namespace, route.Port)
		b.WriteString("}\n")
	}

	// Secret-backed TLS routes: HTTPS with mounted TLS material (no ACME)
	for i, route := range secretTLS {
		if i > 0 || len(direct) > 0 {
			b.WriteString("\n")
		}
		for j, domain := range route.Domains {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(domain)
		}
		b.WriteString(" {\n")
		b.WriteString("\ttls /etc/caddy/tls/tls.crt /etc/caddy/tls/tls.key\n")
		fmt.Fprintf(&b, "\treverse_proxy %s.%s.svc.cluster.local:%d\n", route.Service, namespace, route.Port)
		b.WriteString("}\n")
	}

	return b.String()
}

// ── Caddyfile parsing ─────────────────────────────────────────────────────────

// GetIngressRoutes reads the current Caddy ConfigMap and extracts the routes.
func GetIngressRoutes(ctx context.Context, ssh utils.SSHClient, ns, configMapName string) ([]IngressRoute, error) {
	cmd := kubectl(ns, fmt.Sprintf("get configmap %s -o jsonpath='{.data.Caddyfile}' 2>/dev/null", configMapName))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return nil, nil
	}

	raw := strings.TrimSpace(string(out))
	raw = strings.Trim(raw, "'")
	if raw == "" {
		return nil, nil
	}

	return parseCaddyfile(raw), nil
}

func parseCaddyfile(content string) []IngressRoute {
	var routes []IngressRoute
	lines := strings.Split(content, "\n")

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || line == "}" {
			continue
		}

		if strings.HasSuffix(line, "{") {
			domainPart := strings.TrimSuffix(line, "{")
			domainPart = strings.TrimSpace(domainPart)

			domains := strings.Split(domainPart, ",")
			for k := range domains {
				domains[k] = strings.TrimSpace(domains[k])
			}

			route := IngressRoute{Domains: domains}

			// Scan all lines inside the block until closing }
			for i++; i < len(lines); i++ {
				inner := strings.TrimSpace(lines[i])
				if inner == "}" {
					break
				}
				if strings.HasPrefix(inner, "tls /etc/caddy/tls/") {
					route.UseTLSSecret = true
				}
				if strings.HasPrefix(inner, "reverse_proxy ") {
					upstream := strings.TrimPrefix(inner, "reverse_proxy ")
					parts := strings.SplitN(upstream, ".", 2)
					if len(parts) >= 1 {
						route.Service = parts[0]
					}
					if idx := strings.LastIndex(upstream, ":"); idx >= 0 {
						fmt.Sscanf(upstream[idx+1:], "%d", &route.Port)
					}
				}
			}
			routes = append(routes, route)
		}
	}
	return routes
}
