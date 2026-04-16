package kube

import (
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

// GenerateIngressYAML produces a standard k8s Ingress resource.
func GenerateIngressYAML(route IngressRoute, ns string, acme bool) (string, error) {
	pathType := networkingv1.PathTypePrefix
	var rules []networkingv1.IngressRule
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
	}

	annotations := map[string]string{}
	if acme {
		annotations["traefik.ingress.kubernetes.io/router.tls.certresolver"] = "letsencrypt"
	}

	ingress := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeIngressName(route.Service),
			Namespace: ns,
			Labels: map[string]string{
				utils.LabelAppName:      route.Service,
				utils.LabelAppManagedBy: utils.LabelManagedBy,
			},
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{Rules: rules},
	}

	b, err := sigsyaml.Marshal(ingress)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
