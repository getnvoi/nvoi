package ngrok

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

const (
	AgentImage      = "ngrok/ngrok:3.20.0"
	AgentName       = "ngrok"
	AgentSecretName = "ngrok-authtoken"
	AgentConfigName = "ngrok-config"
	agentReplicas   = int32(2)
)

// BuildWorkloads returns the Deployment + ConfigMap + Secret for the ngrok agent.
func BuildWorkloads(name, namespace, authtoken string, labels map[string]string, routes []provider.TunnelRoute) []runtime.Object {
	secret := buildSecret(authtoken, labels)
	cm := buildConfigMap(namespace, labels, routes)
	dep := buildDeployment(labels)
	return []runtime.Object{secret, cm, dep}
}

func buildSecret(authtoken string, labels map[string]string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   AgentSecretName,
			Labels: labels,
		},
		StringData: map[string]string{
			"authtoken": authtoken,
		},
	}
}

func buildConfigMap(namespace string, labels map[string]string, routes []provider.TunnelRoute) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   AgentConfigName,
			Labels: labels,
		},
		Data: map[string]string{
			"ngrok.yml": buildConfig(namespace, routes),
		},
	}
}

// buildConfig renders the ngrok v2 agent config YAML.
// The authtoken is sourced from the NGROK_AUTHTOKEN env var at runtime.
func buildConfig(namespace string, routes []provider.TunnelRoute) string {
	var sb strings.Builder
	sb.WriteString("version: \"2\"\n")
	sb.WriteString("tunnels:\n")
	for _, r := range routes {
		scheme := r.Scheme
		if scheme == "" {
			scheme = "http"
		}
		tunnelName := strings.ReplaceAll(r.Hostname, ".", "-")
		addr := fmt.Sprintf("%s://%s.%s.svc.cluster.local:%d", scheme, r.ServiceName, namespace, r.ServicePort)
		sb.WriteString(fmt.Sprintf("  %s:\n", tunnelName))
		sb.WriteString(fmt.Sprintf("    proto: http\n"))
		sb.WriteString(fmt.Sprintf("    addr: %s\n", addr))
		sb.WriteString(fmt.Sprintf("    hostname: %s\n", r.Hostname))
	}
	return sb.String()
}

func buildDeployment(labels map[string]string) *appsv1.Deployment {
	replicas := agentReplicas
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   AgentName,
			Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{utils.LabelAppName: AgentName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{utils.LabelAppName: AgentName},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  AgentName,
							Image: AgentImage,
							Args: []string{
								"start",
								"--authtoken", "$(NGROK_AUTHTOKEN)",
								"--config", "/etc/ngrok.yml",
								"--all",
							},
							Env: []corev1.EnvVar{
								{
									Name: "NGROK_AUTHTOKEN",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: AgentSecretName},
											Key:                  "authtoken",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "ngrok-config", MountPath: "/etc/ngrok.yml", SubPath: "ngrok.yml"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "ngrok-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: AgentConfigName},
								},
							},
						},
					},
				},
			},
		},
	}
}
