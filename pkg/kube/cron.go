package kube

import (
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/utils"
)

type CronSpec struct {
	Name          string
	Schedule      string
	Image         string
	Command       string
	Env           []corev1.EnvVar
	SvcSecrets    []string // per-cron secret refs
	SvcSecretName string   // per-cron k8s Secret name ("{cron}-secrets")
	Volumes       []string
	Servers       []string
}

func GenerateCronYAML(spec CronSpec, names *utils.Names, managedVolPaths map[string]string) (string, error) {
	ns := names.KubeNamespace()
	labels := map[string]string{
		utils.LabelAppName:      spec.Name,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
		utils.LabelNvoiService:  spec.Name,
	}

	envVars := append([]corev1.EnvVar{}, spec.Env...)
	for _, ref := range spec.SvcSecrets {
		envName, secretKey := ParseSecretRef(ref)
		envVars = append(envVars, corev1.EnvVar{
			Name: envName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: spec.SvcSecretName},
					Key:                  secretKey,
				},
			},
		})
	}

	container := corev1.Container{
		Name:  spec.Name,
		Image: spec.Image,
		Env:   envVars,
	}
	if spec.Command != "" {
		container.Command = []string{"/bin/sh", "-c"}
		container.Args = []string{spec.Command}
	}

	volumes, mounts, err := buildVolumes(spec.Volumes, names, managedVolPaths)
	if err != nil {
		return "", err
	}

	container.VolumeMounts = mounts

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyOnFailure,
		Containers:    []corev1.Container{container},
		Volumes:       volumes,
	}
	applyNodePlacement(&podSpec, spec.Name, spec.Servers)

	job := batchv1.CronJob{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "CronJob"},
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns, Labels: labels},
		Spec: batchv1.CronJobSpec{
			Schedule: spec.Schedule,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec:       podSpec,
					},
				},
			},
		},
	}

	b, err := sigsyaml.Marshal(job)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
