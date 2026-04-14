package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	Secrets       []string
	SecretName    string
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

// CreateJobFromCronJob creates a one-off Job from an existing CronJob.
// Uses kubectl create job --from=cronjob/<name>.
func CreateJobFromCronJob(ctx context.Context, ssh utils.SSHClient, ns, cronName, jobName string) error {
	cmd := kctl(ns, fmt.Sprintf("create job %s --from=cronjob/%s", jobName, cronName))
	if _, err := ssh.Run(ctx, cmd); err != nil {
		return fmt.Errorf("create job from cronjob/%s: %w", cronName, err)
	}
	return nil
}

// WaitForJob polls a Job's pods until the job succeeds or fails.
// Detects terminal failures (CrashLoopBackOff, BackOff, OOMKilled) immediately
// and returns the container logs on failure. Same pattern as WaitRollout.
func WaitForJob(ctx context.Context, ssh utils.SSHClient, ns, jobName string, emitter ProgressEmitter) error {
	selector := fmt.Sprintf("job-name=%s", jobName)
	lastStatus := ""

	return utils.Poll(ctx, 3*time.Second, 5*time.Minute, func() (bool, error) {
		// Check job completion status first.
		jobOut, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("get job %s -o json", jobName)))
		if err != nil {
			return false, nil
		}
		var job struct {
			Status struct {
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"status"`
		}
		if json.Unmarshal(jobOut, &job) == nil {
			if job.Status.Succeeded > 0 {
				return true, nil
			}
			if job.Status.Failed > 0 {
				logs := RecentLogs(ctx, ssh, ns, jobName, "", 30)
				return false, fmt.Errorf("job %s failed\nlogs:\n%s", jobName, indent(logs, "  "))
			}
		}

		// Poll pods for terminal container states.
		cmd := kctl(ns, fmt.Sprintf("get pods -l %s -o json", selector))
		out, err := ssh.Run(ctx, cmd)
		if err != nil {
			return false, nil
		}
		var pods podList
		if json.Unmarshal(out, &pods) != nil {
			return false, nil
		}

		for _, pod := range pods.Items {
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					reason := cs.State.Waiting.Reason
					switch reason {
					case "CrashLoopBackOff", "BackOff":
						logs := RecentLogs(ctx, ssh, ns, pod.Metadata.Name, "", 30)
						return false, fmt.Errorf("job %s: %s\nlogs:\n%s", jobName, reason, indent(logs, "  "))
					case "ImagePullBackOff", "ErrImagePull":
						return false, fmt.Errorf("job %s: %s — %s", jobName, reason, cs.State.Waiting.Message)
					case "CreateContainerConfigError":
						return false, fmt.Errorf("job %s: %s — %s", jobName, reason, cs.State.Waiting.Message)
					}
				}
				if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
					return false, fmt.Errorf("job %s: OOMKilled", jobName)
				}
			}
		}

		status := fmt.Sprintf("job %s running", jobName)
		if status != lastStatus {
			emitter.Progress(status)
			lastStatus = status
		}
		return false, nil
	})
}

func DeleteCronByName(ctx context.Context, ssh utils.SSHClient, ns, name string) error {
	if _, err := ssh.Run(ctx, kctl(ns, fmt.Sprintf("delete cronjob/%s --ignore-not-found", name))); err != nil {
		return fmt.Errorf("delete cronjob/%s: %w", name, err)
	}
	return nil
}
