package kube

import (
	"context"
	"fmt"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// helmChartConfigGVK identifies the k3s Helm controller's HelmChartConfig CRD.
var helmChartConfigGVK = schema.GroupVersionKind{
	Group: "helm.cattle.io", Version: "v1", Kind: "HelmChartConfig",
}

// IngressRoute maps a service to its public domains.
type IngressRoute struct {
	Service string
	Port    int
	Domains []string
}

// KubeIngressName returns the deterministic Ingress resource name for a service.
func KubeIngressName(service string) string { return "ingress-" + service }

// BuildIngress constructs a typed Ingress for one service. Returned object is
// ready to pass to Client.Apply.
func BuildIngress(route IngressRoute, ns string, acme bool) *networkingv1.Ingress {
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

	ingress := &networkingv1.Ingress{
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
	return ingress
}

// ApplyIngress server-side applies an Ingress for a service.
func (c *Client) ApplyIngress(ctx context.Context, ns string, route IngressRoute, acme bool) error {
	return c.Apply(ctx, ns, BuildIngress(route, ns, acme))
}

// DeleteIngress removes the Ingress resource for a service.
func (c *Client) DeleteIngress(ctx context.Context, ns, service string) error {
	name := KubeIngressName(service)
	err := c.cs.NetworkingV1().Ingresses(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete ingress %s: %w", name, err)
	}
	return nil
}

// GetIngressRoutes lists all nvoi-managed Ingress resources and parses them
// into routes (one IngressRoute per Service, aggregating multiple domains).
func (c *Client) GetIngressRoutes(ctx context.Context, ns string) ([]IngressRoute, error) {
	list, err := c.cs.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{
		LabelSelector: NvoiSelector,
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list ingresses in %s: %w", ns, err)
	}

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

// EnsureTraefikACME applies a HelmChartConfig to configure Traefik's
// Let's Encrypt resolver via the dynamic client (HelmChartConfig is a CRD,
// not in the typed clientset).
//
// When acme is false, no ACME resolver is configured (HTTP-only mode for
// tunnel setups).
//
// Waits for the Traefik deployment to be ready afterwards: the k3s Helm
// controller picks up HelmChartConfig changes asynchronously, so without
// this wait Ingress resources applied immediately after may land before
// Traefik has loaded the ACME certresolver config.
func (c *Client) EnsureTraefikACME(ctx context.Context, email string, acme bool) error {
	var valuesContent string
	if acme {
		valuesContent = fmt.Sprintf(`additionalArguments:
  - "--certificatesresolvers.letsencrypt.acme.email=%s"
  - "--certificatesresolvers.letsencrypt.acme.storage=/data/acme.json"
  - "--certificatesresolvers.letsencrypt.acme.httpchallenge=true"
  - "--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web"`, email)
	} else {
		valuesContent = `ports:
  websecure:
    expose:
      default: false`
	}

	hcc := &unstructured.Unstructured{}
	hcc.SetGroupVersionKind(helmChartConfigGVK)
	hcc.SetName("traefik")
	hcc.SetNamespace("kube-system")
	if err := unstructured.SetNestedField(hcc.Object, valuesContent, "spec", "valuesContent"); err != nil {
		return fmt.Errorf("build helmchartconfig: %w", err)
	}

	if err := c.Apply(ctx, "kube-system", hcc); err != nil {
		return err
	}

	return c.waitForTraefikReady(ctx)
}

// waitForTraefikReady polls until the Traefik deployment in kube-system has
// all replicas available.
func (c *Client) waitForTraefikReady(ctx context.Context) error {
	return utils.Poll(ctx, rolloutPollInterval, 2*time.Minute, func() (bool, error) {
		dep, err := c.cs.AppsV1().Deployments("kube-system").Get(ctx, "traefik", metav1.GetOptions{})
		if err != nil {
			return false, nil // deployment may not exist yet — retry
		}
		desired := int32(0)
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}
		ready := dep.Status.ReadyReplicas
		return desired > 0 && ready == desired, nil
	})
}
