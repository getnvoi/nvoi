package kube

import (
	"context"
	"errors"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

type CronSpec struct {
	Name           string
	Schedule       string
	Image          string
	Command        string
	Env            []corev1.EnvVar
	SvcSecrets     []string // per-cron secret refs
	SvcSecretName  string   // per-cron k8s Secret name ("{cron}-secrets")
	Volumes        []string
	Servers        []string
	PullSecretName string // optional imagePullSecrets reference; empty = no pull auth
}

// BuildCronJob constructs a typed batchv1.CronJob from a CronSpec.
// The returned object is ready to pass to Client.Apply.
func BuildCronJob(spec CronSpec, names *utils.Names, managedVolPaths map[string]string) (*batchv1.CronJob, error) {
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
		return nil, err
	}
	container.VolumeMounts = mounts

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyOnFailure,
		Containers:    []corev1.Container{container},
		Volumes:       volumes,
	}
	if spec.PullSecretName != "" {
		podSpec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: spec.PullSecretName}}
	}
	applyNodePlacement(&podSpec, spec.Name, spec.Servers)

	return &batchv1.CronJob{
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
	}, nil
}

// CreateJobFromCronJob creates a one-off Job from an existing CronJob's
// template. Equivalent to `kubectl create job --from=cronjob/<name>` but
// without shelling out.
func (c *Client) CreateJobFromCronJob(ctx context.Context, ns, cronName, jobName string) error {
	cron, err := c.cs.BatchV1().CronJobs(ns).Get(ctx, cronName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get cronjob/%s: %w", cronName, err)
	}
	annotations := map[string]string{"cronjob.kubernetes.io/instantiate": "manual"}
	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName,
			Namespace:   ns,
			Labels:      cron.Spec.JobTemplate.Labels,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "CronJob",
				Name:       cron.Name,
				UID:        cron.UID,
			}},
		},
		Spec: cron.Spec.JobTemplate.Spec,
	}
	if _, err := c.cs.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{FieldManager: FieldManager}); err != nil {
		return fmt.Errorf("create job/%s from cronjob/%s: %w", jobName, cronName, err)
	}
	return nil
}

// WaitForJob polls a Job's pods until the job succeeds or fails. Detects
// terminal failures (CrashLoopBackOff, BackOff, OOMKilled) immediately and
// returns the container logs on failure. Same shape as WaitRollout.
func (c *Client) WaitForJob(ctx context.Context, ns, jobName string, emitter ProgressEmitter) error {
	selector := fmt.Sprintf("job-name=%s", jobName)
	lastStatus := ""

	return utils.Poll(ctx, rolloutPollInterval, 5*time.Minute, func() (bool, error) {
		job, err := c.cs.BatchV1().Jobs(ns).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, nil
		}
		if job != nil {
			if job.Status.Succeeded > 0 {
				return true, nil
			}
			if job.Status.Failed > 0 {
				logs := c.RecentLogs(ctx, ns, jobName, "", 30)
				return false, fmt.Errorf("job %s failed\nlogs:\n%s", jobName, indent(logs, "  "))
			}
		}

		pods, err := c.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return false, nil
		}

		for _, pod := range pods.Items {
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					reason := cs.State.Waiting.Reason
					switch reason {
					case "CrashLoopBackOff", "BackOff":
						logs := c.RecentLogs(ctx, ns, pod.Name, "", 30)
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

// DeleteCronByName removes a CronJob by name. Idempotent.
func (c *Client) DeleteCronByName(ctx context.Context, ns, name string) error {
	err := c.cs.BatchV1().CronJobs(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.Is(err, apierrors.NewNotFound(batchv1.Resource("cronjobs"), name)) || apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete cronjob/%s: %w", name, err)
	}
	return nil
}
