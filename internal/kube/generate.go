package kube

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/getnvoi/nvoi/internal/core"
)

// ServiceSpec describes a service to deploy.
type ServiceSpec struct {
	Name       string
	Image      string
	Port       int
	Command    string
	Replicas   int
	Env        []corev1.EnvVar
	Volumes    []string // "pgdata:/var/lib/postgresql/data"
	HealthPath string
	Server     string // node selector (empty = any)
	Managed    bool   // true if any volume is provider-managed → StatefulSet
}

// GenerateYAML produces k8s YAML for a single service: workload + Service.
func GenerateYAML(spec ServiceSpec, names *core.Names, managedVolPaths map[string]string) (string, string, error) {
	ns := names.KubeNamespace()
	labels := map[string]string{
		core.LabelAppName:     spec.Name,
		core.LabelAppManagedBy: core.LabelManagedBy,
		core.LabelNvoiService: spec.Name,
	}

	// Container
	container := corev1.Container{
		Name:  spec.Name,
		Image: spec.Image,
		Env:   spec.Env,
	}
	if spec.Command != "" {
		container.Command = []string{"/bin/sh", "-c"}
		container.Args = []string{spec.Command}
	}
	if spec.Port > 0 {
		container.Ports = []corev1.ContainerPort{{ContainerPort: int32(spec.Port)}}
		if spec.HealthPath != "" {
			container.ReadinessProbe = &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: spec.HealthPath,
						Port: intstr.FromInt32(int32(spec.Port)),
					},
				},
				InitialDelaySeconds: 10,
				PeriodSeconds:       5,
				TimeoutSeconds:      3,
			}
		}
	}

	// Volumes
	volumes, mounts, err := buildVolumes(spec.Volumes, names, managedVolPaths)
	if err != nil {
		return "", "", err
	}
	container.VolumeMounts = mounts

	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{container},
		Volumes:    volumes,
	}
	if spec.Server != "" {
		podSpec.NodeSelector = map[string]string{core.LabelNvoiRole: spec.Server}
	}

	// Workload: StatefulSet or Deployment
	var workloadKind string
	var docs []string

	if spec.Managed {
		workloadKind = "statefulset"
		one := int32(1)
		ss := appsv1.StatefulSet{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
			ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns, Labels: labels},
			Spec: appsv1.StatefulSetSpec{
				ServiceName: spec.Name,
				Replicas:    &one,
				Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{core.LabelAppName: spec.Name}},
				Template:    corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: podSpec},
			},
		}
		b, err := sigsyaml.Marshal(ss)
		if err != nil {
			return "", "", err
		}
		docs = append(docs, strings.TrimSpace(string(b)))
	} else {
		workloadKind = "deployment"
		replicas := int32(spec.Replicas)
		if replicas < 1 {
			replicas = 1
		}
		dep := appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns, Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{core.LabelAppName: spec.Name}},
				Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: podSpec},
			},
		}
		b, err := sigsyaml.Marshal(dep)
		if err != nil {
			return "", "", err
		}
		docs = append(docs, strings.TrimSpace(string(b)))
	}

	// Service
	svc := corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns, Labels: labels},
		Spec:       svcSpec(spec.Name, spec.Port),
	}
	b, err := sigsyaml.Marshal(svc)
	if err != nil {
		return "", "", err
	}
	docs = append(docs, strings.TrimSpace(string(b)))

	return strings.Join(docs, "\n---\n"), workloadKind, nil
}

func svcSpec(selector string, port int) corev1.ServiceSpec {
	spec := corev1.ServiceSpec{
		Selector: map[string]string{core.LabelAppName: selector},
	}
	if port > 0 {
		spec.Ports = []corev1.ServicePort{{
			Port:       int32(port),
			TargetPort: intstr.FromInt32(int32(port)),
		}}
	} else {
		spec.ClusterIP = "None"
	}
	return spec
}

func buildVolumes(mounts []string, names *core.Names, managedVolPaths map[string]string) ([]corev1.Volume, []corev1.VolumeMount, error) {
	var volumes []corev1.Volume
	var vms []corev1.VolumeMount
	hostPathType := corev1.HostPathDirectoryOrCreate

	for i, mount := range mounts {
		source, target, named, ok := core.ParseVolumeMount(mount)
		if !ok {
			return nil, nil, fmt.Errorf("invalid volume mount %q", mount)
		}
		volName := fmt.Sprintf("vol-%d", i)
		hostPath := source

		if named {
			if path, ok := managedVolPaths[source]; ok {
				hostPath = path
			} else {
				hostPath = names.NamedVolumeHostPath(source)
			}
		} else if strings.HasPrefix(source, ".") {
			return nil, nil, fmt.Errorf("relative bind mount %q not supported", mount)
		}

		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: hostPath, Type: &hostPathType},
			},
		})
		vms = append(vms, corev1.VolumeMount{Name: volName, MountPath: target})
	}
	return volumes, vms, nil
}
