package kube

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// Caddy in-cluster constants. Single source of truth — every test, every
// reconcile reads from here.
const (
	CaddyNamespace      = "kube-system"
	CaddyName           = "caddy"
	CaddyConfigMapName  = "caddy-config"
	CaddyPVCName        = "caddy-data"
	CaddyImage          = "caddy:2.10-alpine"
	CaddyAdminListen    = "localhost:2019"
	CaddyConfigMountDir = "/etc/caddy"
	CaddyConfigKey      = "caddy.json"
	CaddyDataDir        = "/data"
	CaddyConfigStateDir = "/config"
)

// caddyLabels are applied to every Caddy resource so they can be identified
// and selected together. Same shape as the rest of nvoi-managed resources
// but in kube-system rather than the app namespace.
func caddyLabels() map[string]string {
	return map[string]string{
		utils.LabelAppName:      CaddyName,
		utils.LabelAppManagedBy: utils.LabelManagedBy,
	}
}

// caddySeedConfigJSON is the minimal admin-only Caddy config baked into the
// seed ConfigMap. The pod boots with admin listening on localhost:2019 and
// no servers — the reconciler immediately POSTs the real config via the
// admin API, swapping in routes + TLS automation atomically.
const caddySeedConfigJSON = `{"admin":{"listen":"localhost:2019"}}`

// buildCaddyPVC returns a 1Gi PVC for /data (ACME certs + Caddy state).
// Storage class left unset → k3s default (local-path) takes over.
func buildCaddyPVC() *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CaddyPVCName,
			Namespace: CaddyNamespace,
			Labels:    caddyLabels(),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
}

// buildCaddyConfigMap returns the seed ConfigMap. Holds an admin-only
// caddy.json so the pod can start before the reconciler reaches it. The
// ConfigMap is never updated post-creation — real config flows through the
// admin API.
func buildCaddyConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CaddyConfigMapName,
			Namespace: CaddyNamespace,
			Labels:    caddyLabels(),
		},
		Data: map[string]string{
			CaddyConfigKey: caddySeedConfigJSON,
		},
	}
}

// buildCaddyService returns the cluster Service for Caddy. ports 80/443 for
// internal probes; real traffic enters via hostPort on the master node, not
// through this Service. The admin listener is intentionally NOT exposed.
func buildCaddyService() *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CaddyName,
			Namespace: CaddyNamespace,
			Labels:    caddyLabels(),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{utils.LabelAppName: CaddyName},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP},
				{Name: "https", Port: 443, TargetPort: intstr.FromInt(443), Protocol: corev1.ProtocolTCP},
			},
		},
	}
}

// buildCaddyDeployment returns the single-replica Caddy Deployment pinned to
// the master node via nodeSelector. hostPort 80/443 binds the master's
// public NIC directly — no NodePort, no LoadBalancer. /data and /config are
// persistent (PVC + emptyDir) so cert state and admin autosave survive pod
// restarts.
func buildCaddyDeployment() *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CaddyName,
			Namespace: CaddyNamespace,
			Labels:    caddyLabels(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Strategy: appsv1.DeploymentStrategy{
				// Single replica with hostPort — only one pod can hold the
				// port at a time. Recreate avoids a second pod fighting for
				// :80/:443 during rolls.
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{utils.LabelAppName: CaddyName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: caddyLabels()},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{utils.LabelNvoiRole: utils.RoleMaster},
					// Tolerate the standard control-plane taint so the pod
					// schedules on a single-master k3s cluster where the
					// master also acts as a worker.
					Tolerations: []corev1.Toleration{
						{Key: "node-role.kubernetes.io/control-plane", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
						{Key: "node-role.kubernetes.io/master", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
					},
					Containers: []corev1.Container{{
						Name:    CaddyName,
						Image:   CaddyImage,
						Command: []string{"caddy"},
						// --adapter "" disables the Caddyfile adapter so Caddy
						// reads /etc/caddy/caddy.json as native JSON.
						Args: []string{"run", "--config", CaddyConfigMountDir + "/" + CaddyConfigKey, "--adapter", ""},
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: 80, HostPort: 80, Protocol: corev1.ProtocolTCP},
							{Name: "https", ContainerPort: 443, HostPort: 443, Protocol: corev1.ProtocolTCP},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: CaddyConfigMountDir, ReadOnly: true},
							{Name: "data", MountPath: CaddyDataDir},
							{Name: "state", MountPath: CaddyConfigStateDir},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{
									Port: intstr.FromInt(80),
								},
							},
							InitialDelaySeconds: 2,
							PeriodSeconds:       5,
							TimeoutSeconds:      3,
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: CaddyConfigMapName},
								},
							},
						},
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: CaddyPVCName,
								},
							},
						},
						{
							Name:         "state",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
				},
			},
		},
	}
}
