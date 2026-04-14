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

func generateManifests(name, svcName, secretName, volumeMountPath string, engine Engine, image, ns string, server string) string {
	prefix := strings.ToUpper(name)

	labels := map[string]string{
		utils.LabelAppName:      svcName,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
	}

	one := int32(1)
	hostPathType := corev1.HostPathDirectoryOrCreate
	port := engine.Port()

	containerEnv := engine.ContainerEnv(secretName, prefix)
	if envName, path, needed := engine.DataDir(); needed {
		containerEnv = append(containerEnv, corev1.EnvVar{Name: envName, Value: path})
	}

	_, mountPath, _ := engine.DataDir()
	if mountPath == "" {
		mountPath = "/var/lib/data"
	}
	// Mount parent dir (e.g., /var/lib/postgresql/data not /var/lib/postgresql/data/pgdata)
	mountDir := mountPath
	if idx := strings.LastIndex(mountDir, "/"); idx > 0 && strings.Count(mountDir, "/") > 3 {
		mountDir = mountDir[:idx]
	}

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
						Ports: []corev1.ContainerPort{{ContainerPort: port}},
						Env:   containerEnv,
						VolumeMounts: []corev1.VolumeMount{{
							Name: "data", MountPath: mountDir,
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								Exec: &corev1.ExecAction{Command: engine.ReadinessProbe("$(POSTGRES_USER)")},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       5,
						},
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: volumeMountPath,
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
			Ports:     []corev1.ServicePort{{Port: port, TargetPort: intstr.FromInt(int(port))}},
		},
	}

	ssYAML, _ := sigsyaml.Marshal(ss)
	svcYAML, _ := sigsyaml.Marshal(svc)
	return strings.TrimSpace(string(ssYAML)) + "\n---\n" + strings.TrimSpace(string(svcYAML))
}

func applyManifest(ctx context.Context, ssh utils.SSHClient, ns, manifest string) error {
	return kube.Apply(ctx, ssh, ns, manifest)
}

func waitReady(ctx context.Context, ssh utils.SSHClient, ns, svcName string, engine Engine, user string) error {
	pod := svcName + "-0"
	probeCmd := engine.ReadinessProbe(user)
	cmd := fmt.Sprintf("exec %s -- %s", pod, strings.Join(probeCmd, " "))
	return utils.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		_, err := kube.RunKubectl(ctx, ssh, ns, cmd)
		return err == nil, nil
	})
}
