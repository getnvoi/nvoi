package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// IngressRoute maps a service to its public domains.
type IngressRoute struct {
	Service string
	Port    int
	Domains []string
}

// KubeIngressName returns the deterministic Ingress resource name for a service.
func KubeIngressName(service string) string { return "ingress-" + service }

// ApplyIngress creates or updates a standard k8s Ingress resource for a service.
// One Ingress per service — no shared state, no read-modify-write.
func ApplyIngress(ctx context.Context, ssh utils.SSHClient, ns string, route IngressRoute, acme bool) error {
	yaml, err := GenerateIngressYAML(route, ns, acme)
	if err != nil {
		return err
	}
	return Apply(ctx, ssh, ns, yaml)
}

// GenerateIngressYAML produces a standard networking.k8s.io/v1 Ingress resource.
func GenerateIngressYAML(route IngressRoute, ns string, acme bool) (string, error) {
	name := KubeIngressName(route.Service)
	pathType := networkingv1.PathTypePrefix

	annotations := map[string]string{
		utils.LabelAppManagedBy:                            utils.LabelManagedBy,
		"traefik.ingress.kubernetes.io/router.entrypoints": "web,websecure",
	}
	if acme {
		annotations["traefik.ingress.kubernetes.io/router.tls.certresolver"] = "letsencrypt"
	}

	var rules []networkingv1.IngressRule
	var tlsHosts []string
	for _, domain := range route.Domains {
		rules = append(rules, networkingv1.IngressRule{
			Host: domain,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path:     "/",
						PathType: &pathType,
						Backend: networkingv1.IngressBackend{
							Service: &networkingv1.IngressServiceBackend{
								Name: route.Service,
								Port: networkingv1.ServiceBackendPort{Number: int32(route.Port)},
							},
						},
					}},
				},
			},
		})
		tlsHosts = append(tlsHosts, domain)
	}

	ingress := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				utils.LabelAppName:      name,
				utils.LabelAppManagedBy: utils.LabelManagedBy,
			},
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			Rules: rules,
		},
	}

	if acme {
		ingress.Spec.TLS = []networkingv1.IngressTLS{{
			Hosts:      tlsHosts,
			SecretName: "tls-" + route.Service,
		}}
	}

	b, err := sigsyaml.Marshal(ingress)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// DeleteIngress removes the Ingress resource for a service.
func DeleteIngress(ctx context.Context, ssh utils.SSHClient, ns, service string) error {
	name := KubeIngressName(service)
	_, err := RunKubectl(ctx, ssh, ns, fmt.Sprintf("delete ingress %s --ignore-not-found", name))
	return err
}

// GetIngressRoutes lists all nvoi-managed Ingress resources and parses them into routes.
func GetIngressRoutes(ctx context.Context, ssh utils.SSHClient, ns string) ([]IngressRoute, error) {
	out, err := RunKubectl(ctx, ssh, ns, fmt.Sprintf("get ingress -l %s=%s -o json 2>/dev/null", utils.LabelAppManagedBy, utils.LabelManagedBy))
	if err != nil {
		return nil, nil
	}

	var list networkingv1.IngressList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, nil
	}

	// Group by service name.
	byService := map[string]*IngressRoute{}
	for _, item := range list.Items {
		for _, rule := range item.Spec.Rules {
			if rule.HTTP == nil || len(rule.HTTP.Paths) == 0 {
				continue
			}
			path := rule.HTTP.Paths[0]
			if path.Backend.Service == nil {
				continue
			}
			svc := path.Backend.Service.Name
			port := int(path.Backend.Service.Port.Number)
			if _, ok := byService[svc]; !ok {
				byService[svc] = &IngressRoute{Service: svc, Port: port}
			}
			byService[svc].Domains = append(byService[svc].Domains, rule.Host)
		}
	}

	var routes []IngressRoute
	for _, r := range byService {
		routes = append(routes, *r)
	}
	return routes, nil
}

// EnsureTraefikACME applies a HelmChartConfig to configure Traefik's Let's Encrypt resolver.
// Idempotent — safe to call multiple times.
// When acme is false, no ACME resolver is configured (HTTP-only mode for tunnel setups).
func EnsureTraefikACME(ctx context.Context, ssh utils.SSHClient, email string, acme bool) error {
	var valuesContent string
	if acme {
		valuesContent = fmt.Sprintf(`    additionalArguments:
      - "--certificatesresolvers.letsencrypt.acme.email=%s"
      - "--certificatesresolvers.letsencrypt.acme.storage=/data/acme.json"
      - "--certificatesresolvers.letsencrypt.acme.httpchallenge=true"
      - "--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web"`, email)
	} else {
		valuesContent = `    ports:
      websecure:
        expose:
          default: false`
	}

	yaml := fmt.Sprintf(`apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |-
%s`, valuesContent)

	// HelmChartConfig is cluster-scoped in kube-system, apply without namespace flag.
	_, err := ssh.Run(ctx, fmt.Sprintf("cat <<'EOYAML' | kubectl apply -f -\n%s\nEOYAML", yaml))
	return err
}
