package kube

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// applyNodePlacement configures pod scheduling based on server list.
// Single server → nodeSelector. Multiple servers → nodeAffinity with In
// operator + topologySpreadConstraints for even distribution.
// Empty → defaults to master.
func applyNodePlacement(podSpec *corev1.PodSpec, name string, servers []string) {
	if len(servers) == 0 {
		servers = []string{"master"}
	}
	if len(servers) == 1 {
		podSpec.NodeSelector = map[string]string{utils.LabelNvoiRole: servers[0]}
		return
	}
	podSpec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      utils.LabelNvoiRole,
						Operator: corev1.NodeSelectorOpIn,
						Values:   servers,
					}},
				}},
			},
		},
	}
	podSpec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
		MaxSkew:           1,
		TopologyKey:       utils.LabelNvoiRole,
		WhenUnsatisfiable: corev1.ScheduleAnyway,
		LabelSelector:     &metav1.LabelSelector{MatchLabels: map[string]string{utils.LabelAppName: name}},
	}}
}

// ParseSecretRef splits a secret reference into env var name and k8s secret key.
// "RAILS_MASTER_KEY" → ("RAILS_MASTER_KEY", "RAILS_MASTER_KEY")
// "POSTGRES_PASSWORD=POSTGRES_PASSWORD_DB" → ("POSTGRES_PASSWORD", "POSTGRES_PASSWORD_DB")
func ParseSecretRef(ref string) (envName, secretKey string) {
	if envName, secretKey, ok := strings.Cut(ref, "="); ok {
		return envName, secretKey
	}
	return ref, ref
}

// ServiceSpec describes a service to deploy.
type ServiceSpec struct {
	Name          string
	Image         string
	Port          int
	Command       string
	Replicas      int
	Env           []corev1.EnvVar
	SvcSecrets    []string // per-service secret refs → env.valueFrom.secretKeyRef
	SvcSecretName string   // per-service k8s Secret name ("{svc}-secrets")
	Volumes       []string // "pgdata:/var/lib/postgresql/data"
	HealthPath    string
	Servers       []string // node placement (empty = master only)
	Managed       bool     // true if any volume is provider-managed → StatefulSet
}

// BuildService produces typed Workload (Deployment or StatefulSet) and
// Service objects ready to pass to Client.Apply. The string return value is
// the workload kind: "deployment" or "statefulset".
func BuildService(spec ServiceSpec, names *utils.Names, managedVolPaths map[string]string) (workload runtime.Object, svc *corev1.Service, kind string, err error) {
	ns := names.KubeNamespace()
	labels := map[string]string{
		utils.LabelAppName:      spec.Name,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
		utils.LabelNvoiService:  spec.Name,
	}

	// Container
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
	if spec.Port > 0 {
		container.Ports = []corev1.ContainerPort{{ContainerPort: int32(spec.Port)}}
		// Readiness probe:
		//   - user declared a health path → HTTP GET on that path
		//   - otherwise → TCP connect on the port
		//
		// Default TCP probe matters for depends_on ordering: without it,
		// "Ready" collapses to "container is Running", which for a DB
		// means "still initializing, not yet accepting connections". TCP
		// probe ensures Ready == "accepting traffic" so dependents see a
		// truly reachable dep.
		probe := &corev1.Probe{
			InitialDelaySeconds: 5,
			PeriodSeconds:       5,
			TimeoutSeconds:      3,
		}
		if spec.HealthPath != "" {
			probe.ProbeHandler = corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: spec.HealthPath,
					Port: intstr.FromInt32(int32(spec.Port)),
				},
			}
			probe.InitialDelaySeconds = 10
		} else {
			probe.ProbeHandler = corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(int32(spec.Port)),
				},
			}
		}
		container.ReadinessProbe = probe
	}

	// Volumes
	volumes, mounts, err := buildVolumes(spec.Volumes, names, managedVolPaths)
	if err != nil {
		return nil, nil, "", err
	}
	container.VolumeMounts = mounts

	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{container},
		Volumes:    volumes,
	}
	applyNodePlacement(&podSpec, spec.Name, spec.Servers)

	if spec.Managed {
		one := int32(1)
		ss := &appsv1.StatefulSet{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
			ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns, Labels: labels},
			Spec: appsv1.StatefulSetSpec{
				ServiceName: spec.Name,
				Replicas:    &one,
				Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{utils.LabelAppName: spec.Name}},
				Template:    corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: podSpec},
			},
		}
		workload = ss
		kind = "statefulset"
	} else {
		replicas := int32(spec.Replicas)
		if replicas < 1 {
			replicas = 1
		}
		dep := &appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns, Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Strategy: appsv1.DeploymentStrategy{
					Type: appsv1.RollingUpdateDeploymentStrategyType,
					RollingUpdate: &appsv1.RollingUpdateDeployment{
						MaxUnavailable: intOrStr(0),
						MaxSurge:       intOrStr(1),
					},
				},
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{utils.LabelAppName: spec.Name}},
				Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: podSpec},
			},
		}
		workload = dep
		kind = "deployment"
	}

	svc = &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns, Labels: labels},
		Spec:       svcSpec(spec.Name, spec.Port),
	}
	return workload, svc, kind, nil
}

func svcSpec(selector string, port int) corev1.ServiceSpec {
	spec := corev1.ServiceSpec{
		Selector: map[string]string{utils.LabelAppName: selector},
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

func buildVolumes(mounts []string, names *utils.Names, managedVolPaths map[string]string) ([]corev1.Volume, []corev1.VolumeMount, error) {
	var volumes []corev1.Volume
	var vms []corev1.VolumeMount
	hostPathType := corev1.HostPathDirectoryOrCreate

	for i, mount := range mounts {
		source, target, named, ok := utils.ParseVolumeMount(mount)
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

func intOrStr(val int) *intstr.IntOrString {
	v := intstr.FromInt32(int32(val))
	return &v
}
