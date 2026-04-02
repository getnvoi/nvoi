package kube

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/core"
)

// IngressRoute maps a service to its public domains.
type IngressRoute struct {
	Service string
	Port    int
	Domains []string
}

// GenerateCaddyManifest produces the Caddy ConfigMap + Deployment YAML.
// Caddy runs with hostNetwork on the master node, handling TLS via Let's Encrypt.
func GenerateCaddyManifest(routes []IngressRoute, names *core.Names) (string, error) {
	if len(routes) == 0 {
		return "", nil
	}

	ns := names.KubeNamespace()
	caddyfile := generateCaddyfile(routes, ns)
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(caddyfile)))

	// ConfigMap
	cm := corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: names.KubeCaddyConfig(), Namespace: ns},
		Data:       map[string]string{"Caddyfile": caddyfile},
	}
	cmBytes, err := sigsyaml.Marshal(cm)
	if err != nil {
		return "", err
	}

	// Deployment
	one := int32(1)
	hostPathType := corev1.HostPathDirectoryOrCreate
	caddyName := names.KubeCaddy()
	labels := map[string]string{
		core.LabelAppName:      caddyName,
		core.LabelAppManagedBy: core.LabelManagedBy,
	}

	dep := appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: caddyName, Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{core.LabelAppName: caddyName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{core.LabelConfigChecksum: checksum},
				},
				Spec: corev1.PodSpec{
					HostNetwork: true,
					DNSPolicy:   corev1.DNSClusterFirstWithHostNet,
					NodeSelector: map[string]string{
						core.LabelNvoiRole: core.RoleMaster,
					},
					Containers: []corev1.Container{{
						Name:  caddyName,
						Image: core.CaddyImage,
						Ports: []corev1.ContainerPort{
							{ContainerPort: 80, HostPort: 80},
							{ContainerPort: 443, HostPort: 443},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: "/etc/caddy/Caddyfile", SubPath: "Caddyfile", ReadOnly: true},
							{Name: "data", MountPath: "/data"},
						},
					}},
					Volumes: []corev1.Volume{
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
					},
				},
			},
		},
	}
	depBytes, err := sigsyaml.Marshal(dep)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(cmBytes)) + "\n---\n" + strings.TrimSpace(string(depBytes)), nil
}

// generateCaddyfile produces a Caddyfile routing domains to k8s services.
// Uses the namespace-qualified service DNS name so Caddy can resolve via cluster DNS.
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
	for i, route := range sorted {
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
		// Use namespace-qualified service name for cluster DNS resolution
		fmt.Fprintf(&b, "\treverse_proxy %s.%s.svc.cluster.local:%d\n", route.Service, namespace, route.Port)
		b.WriteString("}\n")
	}
	return b.String()
}

// GetIngressRoutes reads the current Caddy ConfigMap and extracts the routes.
// Returns nil if no ConfigMap exists.
func GetIngressRoutes(ctx context.Context, ssh core.SSHClient, ns, configMapName string) ([]IngressRoute, error) {
	cmd := kubectl(ns, fmt.Sprintf("get configmap %s -o jsonpath='{.data.Caddyfile}' 2>/dev/null", configMapName))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return nil, nil // no configmap = no routes
	}

	raw := strings.TrimSpace(string(out))
	raw = strings.Trim(raw, "'")
	if raw == "" {
		return nil, nil
	}

	return parseCaddyfile(raw), nil
}

// parseCaddyfile extracts IngressRoutes from a Caddyfile string.
func parseCaddyfile(content string) []IngressRoute {
	var routes []IngressRoute
	lines := strings.Split(content, "\n")

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || line == "}" {
			continue
		}

		// Domain line ends with {
		if strings.HasSuffix(line, "{") {
			domainPart := strings.TrimSuffix(line, "{")
			domainPart = strings.TrimSpace(domainPart)
			domains := strings.Split(domainPart, ",")
			for k := range domains {
				domains[k] = strings.TrimSpace(domains[k])
			}

			// Next line should be reverse_proxy
			route := IngressRoute{Domains: domains}
			if i+1 < len(lines) {
				proxyLine := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(proxyLine, "reverse_proxy ") {
					upstream := strings.TrimPrefix(proxyLine, "reverse_proxy ")
					// Parse service.namespace.svc.cluster.local:port
					parts := strings.SplitN(upstream, ".", 2)
					if len(parts) >= 1 {
						route.Service = parts[0]
					}
					if idx := strings.LastIndex(upstream, ":"); idx >= 0 {
						fmt.Sscanf(upstream[idx+1:], "%d", &route.Port)
					}
				}
				i++
			}
			routes = append(routes, route)
		}
	}
	return routes
}
