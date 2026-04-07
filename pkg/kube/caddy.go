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

	"github.com/getnvoi/nvoi/pkg/utils"
)

// IngressRoute maps a service to its public domains.
type IngressRoute struct {
	Service string
	Port    int
	Domains []string
	Proxy   bool // Cloudflare proxy mode — Caddy serves plain HTTP, no TLS
}

// GenerateCaddyManifest produces the Caddy ConfigMap + Deployment YAML.
// Caddy runs with hostNetwork on the master node, handling TLS via Let's Encrypt.
func GenerateCaddyManifest(routes []IngressRoute, names *utils.Names) (string, error) {
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
		utils.LabelAppName:      caddyName,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
	}

	dep := appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: caddyName, Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{utils.LabelAppName: caddyName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{utils.LabelConfigChecksum: checksum},
				},
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
//
// Non-proxied routes get individual domain blocks with auto-TLS (Let's Encrypt).
// Proxied routes share a single :80 block with host matchers — Cloudflare
// terminates TLS at the edge, origin serves plain HTTP.
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

	// Collect proxied routes for the shared :80 block
	var proxied []IngressRoute
	var direct []IngressRoute
	for _, route := range sorted {
		if route.Proxy {
			proxied = append(proxied, route)
		} else {
			direct = append(direct, route)
		}
	}

	// Non-proxied routes: individual domain blocks with auto-TLS
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

	// Proxied routes: shared :80 block with host matchers
	if len(proxied) > 0 {
		if len(direct) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(":80 {\n")
		for _, route := range proxied {
			for _, domain := range route.Domains {
				fmt.Fprintf(&b, "\t@%s_%s host %s\n", route.Service, sanitizeMatcher(domain), domain)
				fmt.Fprintf(&b, "\treverse_proxy @%s_%s %s.%s.svc.cluster.local:%d\n",
					route.Service, sanitizeMatcher(domain), route.Service, namespace, route.Port)
			}
		}
		b.WriteString("}\n")
	}

	return b.String()
}

// sanitizeMatcher converts a domain into a safe Caddy matcher name.
// Replaces dots and hyphens with underscores.
func sanitizeMatcher(domain string) string {
	r := strings.NewReplacer(".", "_", "-", "_")
	return r.Replace(domain)
}

// GetIngressRoutes reads the current Caddy ConfigMap and extracts the routes.
// Returns nil if no ConfigMap exists.
func GetIngressRoutes(ctx context.Context, ssh utils.SSHClient, ns, configMapName string) ([]IngressRoute, error) {
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

			// :80 block = proxied routes with host matchers
			if domainPart == ":80" {
				routes = append(routes, parseProxiedBlock(lines[i+1:])...)
				// Skip to end of block
				for i++; i < len(lines); i++ {
					if strings.TrimSpace(lines[i]) == "}" {
						break
					}
				}
				continue
			}

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

// parseProxiedBlock parses @matcher host + reverse_proxy pairs inside a :80 block.
func parseProxiedBlock(lines []string) []IngressRoute {
	// Collect domain → (service, port) from @matcher lines
	type proxyTarget struct {
		service string
		port    int
	}
	serviceRoutes := map[string]*IngressRoute{} // keyed by service name

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "}" || line == "" {
			break
		}

		// @service_domain host example.com
		if strings.HasPrefix(line, "@") && strings.Contains(line, " host ") {
			parts := strings.SplitN(line, " host ", 2)
			if len(parts) != 2 {
				continue
			}
			domain := strings.TrimSpace(parts[1])

			// Next line: reverse_proxy @matcher upstream
			if i+1 < len(lines) {
				proxyLine := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(proxyLine, "reverse_proxy @") {
					// reverse_proxy @matcher service.ns.svc.cluster.local:port
					fields := strings.Fields(proxyLine)
					if len(fields) >= 3 {
						upstream := fields[2]
						svcParts := strings.SplitN(upstream, ".", 2)
						service := svcParts[0]
						var port int
						if idx := strings.LastIndex(upstream, ":"); idx >= 0 {
							fmt.Sscanf(upstream[idx+1:], "%d", &port)
						}

						r, ok := serviceRoutes[service]
						if !ok {
							r = &IngressRoute{Service: service, Port: port, Proxy: true}
							serviceRoutes[service] = r
						}
						r.Domains = append(r.Domains, domain)
					}
					i++ // skip the reverse_proxy line
				}
			}
		}
	}

	var routes []IngressRoute
	for _, r := range serviceRoutes {
		routes = append(routes, *r)
	}
	return routes
}
