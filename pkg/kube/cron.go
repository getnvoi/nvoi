package kube

import (
	"context"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/pkg/utils"
)

type CronSpec struct {
	Name       string
	Schedule   string
	Image      string
	Command    string
	Env        []corev1.EnvVar
	Secrets    []string
	SecretName string
	Volumes    []string
	HostPaths  []string // host:container:mode mounts (e.g. "/home/deploy/s3upload:/home/deploy/s3upload:ro")
	Server     string
}

func GenerateCronYAML(spec CronSpec, names *utils.Names, managedVolPaths map[string]string) (string, error) {
	ns := names.KubeNamespace()
	labels := map[string]string{
		utils.LabelAppName:      spec.Name,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
		utils.LabelNvoiService:  spec.Name,
	}

	envVars := append([]corev1.EnvVar{}, spec.Env...)
	for _, ref := range spec.Secrets {
		envName, secretKey := ParseSecretRef(ref)
		envVars = append(envVars, corev1.EnvVar{
			Name: envName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: spec.SecretName},
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

	// Add hostPath mounts (e.g. s3upload binary).
	for i, hp := range spec.HostPaths {
		hostPath, containerPath, readOnly := parseHostPath(hp)
		volName := fmt.Sprintf("hostpath-%d", i)
		hostPathType := corev1.HostPathFile
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: hostPath, Type: &hostPathType},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: containerPath,
			ReadOnly:  readOnly,
		})
	}

	container.VolumeMounts = mounts

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyOnFailure,
		Containers:    []corev1.Container{container},
		Volumes:       volumes,
	}
	if spec.Server != "" {
		podSpec.NodeSelector = map[string]string{utils.LabelNvoiRole: spec.Server}
	}

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

// CreateJobFromCronJob creates a one-off Job from an existing CronJob.
// Uses kubectl create job --from=cronjob/<name>.
func CreateJobFromCronJob(ctx context.Context, ssh utils.SSHClient, ns, cronName, jobName string) error {
	cmd := kubectl(ns, fmt.Sprintf("create job %s --from=cronjob/%s", jobName, cronName))
	if _, err := ssh.Run(ctx, cmd); err != nil {
		return fmt.Errorf("create job from cronjob/%s: %w", cronName, err)
	}
	return nil
}

// parseHostPath parses "host:container:mode" into components.
// If mode is "ro", readOnly is true. Default is read-write.
func parseHostPath(spec string) (hostPath, containerPath string, readOnly bool) {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) >= 2 {
		hostPath = parts[0]
		containerPath = parts[1]
	}
	if len(parts) >= 3 && parts[2] == "ro" {
		readOnly = true
	}
	return
}

func DeleteCronByName(ctx context.Context, ssh utils.SSHClient, ns, name string) error {
	if _, err := ssh.Run(ctx, kubectl(ns, fmt.Sprintf("delete cronjob/%s --ignore-not-found", name))); err != nil {
		return fmt.Errorf("delete cronjob/%s: %w", name, err)
	}
	return nil
}
