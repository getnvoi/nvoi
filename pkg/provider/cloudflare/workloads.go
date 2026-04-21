package cloudflare

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	AgentImage      = "cloudflare/cloudflared:2024.8.3"
	AgentName       = "cloudflared"
	AgentSecretName = "cloudflared-token"
	agentReplicas   = int32(2)
)

// BuildWorkloads returns the Deployment + Secret for the cloudflared agent.
func BuildWorkloads(name, tunnelID, token string, labels map[string]string) []runtime.Object {
	secret := buildSecret(token, labels)
	deployment := buildDeployment(labels)
	return []runtime.Object{secret, deployment}
}

func buildSecret(token string, labels map[string]string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   AgentSecretName,
			Labels: labels,
		},
		StringData: map[string]string{
			"token": token,
		},
	}
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
				MatchLabels: map[string]string{"app.kubernetes.io/name": AgentName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app.kubernetes.io/name": AgentName},
				},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
								{
									Weight: 100,
									Preference: corev1.NodeSelectorTerm{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{Key: "nvoi-role", Operator: corev1.NodeSelectorOpIn, Values: []string{"master"}},
										},
									},
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  AgentName,
							Image: AgentImage,
							Args:  []string{"tunnel", "run", "--token", "$(TUNNEL_TOKEN)"},
							Env: []corev1.EnvVar{
								{
									Name: "TUNNEL_TOKEN",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: AgentSecretName},
											Key:                  "token",
										},
									},
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
					},
				},
			},
		},
	}
}
