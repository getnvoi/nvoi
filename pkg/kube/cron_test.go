package kube

import (
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

func TestGenerateCronYAML_Basic(t *testing.T) {
	names := mustNames(t)
	yamlStr, err := GenerateCronYAML(CronSpec{
		Name:       "backup",
		Schedule:   "0 1 * * *",
		Image:      "busybox:latest",
		SecretName: "secrets",
	}, names, nil)
	if err != nil {
		t.Fatalf("GenerateCronYAML: %v", err)
	}
	var cj batchv1.CronJob
	if err := sigsyaml.Unmarshal([]byte(yamlStr), &cj); err != nil {
		t.Fatalf("unmarshal CronJob: %v", err)
	}
	if cj.Spec.Schedule != "0 1 * * *" {
		t.Fatalf("schedule = %q", cj.Spec.Schedule)
	}
	if cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image != "busybox:latest" {
		t.Fatalf("image = %q", cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestGenerateCronYAML_SecretAlias(t *testing.T) {
	names := mustNames(t)
	yamlStr, err := GenerateCronYAML(CronSpec{
		Name:       "backup",
		Schedule:   "0 1 * * *",
		Image:      "busybox:latest",
		Secrets:    []string{"AWS_SECRET_ACCESS_KEY=STORAGE_BACKUPS_SECRET_ACCESS_KEY"},
		SecretName: "secrets",
	}, names, nil)
	if err != nil {
		t.Fatalf("GenerateCronYAML: %v", err)
	}
	if !strings.Contains(yamlStr, "name: AWS_SECRET_ACCESS_KEY") || !strings.Contains(yamlStr, "key: STORAGE_BACKUPS_SECRET_ACCESS_KEY") {
		t.Fatalf("yaml missing aliased secret ref: %s", yamlStr)
	}
}

func TestGenerateCronYAML_CommandWrappingAndNodeSelector(t *testing.T) {
	names := mustNames(t)
	yamlStr, err := GenerateCronYAML(CronSpec{
		Name:       "backup",
		Schedule:   "0 1 * * *",
		Image:      "busybox:latest",
		Command:    "echo hi",
		SecretName: "secrets",
		Server:     "master",
	}, names, nil)
	if err != nil {
		t.Fatalf("GenerateCronYAML: %v", err)
	}
	if !strings.Contains(yamlStr, "- /bin/sh") || !strings.Contains(yamlStr, "- -c") || !strings.Contains(yamlStr, "- echo hi") {
		t.Fatalf("yaml missing shell command wrapping: %s", yamlStr)
	}
	if !strings.Contains(yamlStr, "nvoi-role: master") {
		t.Fatalf("yaml missing node selector: %s", yamlStr)
	}
}
