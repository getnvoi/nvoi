package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func generateManifests(name, image, ns string, names *utils.Names, server string) string {
	svcName := name + "-db"
	secretName := name + "-db-credentials"
	prefix := strings.ToUpper(name)
	volumePath := names.VolumeMountPath(name + "-db")

	labels := map[string]string{
		utils.LabelAppName:      svcName,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
	}

	one := int32(1)
	hostPathType := corev1.HostPathDirectoryOrCreate

	ss := appsv1.StatefulSet{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
		ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: ns, Labels: labels},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &one,
			ServiceName: svcName,
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{utils.LabelAppName: svcName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{utils.LabelNvoiRole: server},
					Containers: []corev1.Container{{
						Name:  svcName,
						Image: image,
						Ports: []corev1.ContainerPort{{ContainerPort: 5432}},
						Env: []corev1.EnvVar{
							secretEnv("POSTGRES_USER", secretName, prefix+"_POSTGRES_USER"),
							secretEnv("POSTGRES_PASSWORD", secretName, prefix+"_POSTGRES_PASSWORD"),
							secretEnv("POSTGRES_DB", secretName, prefix+"_POSTGRES_DB"),
							{Name: "PGDATA", Value: "/var/lib/postgresql/data/pgdata"},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							MountPath: "/var/lib/postgresql/data",
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								Exec: &corev1.ExecAction{
									Command: []string{"pg_isready", "-U", "$(POSTGRES_USER)"},
								},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       5,
						},
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("2Gi"),
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: volumePath,
								Type: &hostPathType,
							},
						},
					}},
				},
			},
		},
	}

	svc := corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: ns, Labels: labels},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  map[string]string{utils.LabelAppName: svcName},
			Ports: []corev1.ServicePort{{
				Port:       5432,
				TargetPort: intstr.FromInt(5432),
			}},
		},
	}

	ssYAML, _ := sigsyaml.Marshal(ss)
	svcYAML, _ := sigsyaml.Marshal(svc)
	return strings.TrimSpace(string(ssYAML)) + "\n---\n" + strings.TrimSpace(string(svcYAML))
}

func secretEnv(envName, secretName, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: envName,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  key,
			},
		},
	}
}

func applyManifest(ctx context.Context, ssh utils.SSHClient, ns, manifest string) error {
	return kube.Apply(ctx, ssh, ns, manifest)
}

func waitReady(ctx context.Context, ssh utils.SSHClient, ns, svcName, user string) error {
	pod := svcName + "-0" // StatefulSet pod naming
	cmd := fmt.Sprintf("exec %s -- pg_isready -U %s", pod, user)
	return utils.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		_, err := kube.RunKubectl(ctx, ssh, ns, cmd)
		return err == nil, nil
	})
}
