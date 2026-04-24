package postgres

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

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ZFS-backed branching primitives.
//
// k8s-standard kinds (PVC, Service, StatefulSet) are built as typed
// structs and applied through our *kube.Client — same path postgres
// uses for its primary workloads. Snapshot-specific kinds
// (VolumeSnapshot, VolumeSnapshotClass) live in
// snapshot.storage.k8s.io/v1 which isn't in core client-go, so we
// pipe YAML to `sudo k3s kubectl apply -f -` on the master shell —
// same pattern as EnsureZFSCSI. Keeps nvoi core free of the
// external-snapshotter vendored client for a narrow-surface feature.

const (
	// SnapshotClassName is the VolumeSnapshotClass every snapshot is
	// created under. Points at the same ZFS CSI driver the
	// StorageClass binds to.
	SnapshotClassName = "nvoi-zfs-snapshots"

	// snapshotAPIVersion pins the VolumeSnapshot API group/version
	// we emit manifests for. `v1` (not `v1beta1`) is GA since
	// snapshotter v5.0.
	snapshotAPIVersion = "snapshot.storage.k8s.io/v1"

	// brLabel identifies workloads created by `nvoi database branch`
	// so list/delete can target them without hitting the source DB.
	brLabel = "nvoi/branch-of"
)

// ── Snapshot primitives (kubectl-on-master, external-snapshotter kinds) ──

// EnsureSnapshotClass installs the VolumeSnapshotClass via kubectl on
// master. Idempotent. Called by Snapshot before creating a snapshot —
// without the class existing, the snapshot request would fail.
func EnsureSnapshotClass(ctx context.Context, masterSSH utils.SSHClient) error {
	if masterSSH == nil {
		return fmt.Errorf("postgres.EnsureSnapshotClass: master ssh required")
	}
	manifest := fmt.Sprintf(`apiVersion: %s
kind: VolumeSnapshotClass
metadata:
  name: %s
  labels:
    app.kubernetes.io/managed-by: nvoi
driver: zfs.csi.openebs.io
deletionPolicy: Delete
`, snapshotAPIVersion, SnapshotClassName)
	return applyManifest(ctx, masterSSH, manifest)
}

// Snapshot creates a user-requested VolumeSnapshot of the DB's PVC.
// Label is optional metadata; when empty a timestamp-based name is
// used. Returns the snapshot's name so callers can later reference it
// for clone/rollback.
//
// Label is validated as a DNS-1123 segment — it lands in an object
// name and in shell-interpolated YAML inside a kubectl here-doc, so
// unvalidated input is both a correctness hazard (kube rejects the
// object) and an injection vector (escape the heredoc). ValidateName
// rejects upper-case, dots, slashes, spaces, and everything else that
// would break either layer.
//
// Concurrency note: VolumeSnapshot creation is async on the CSI
// controller's side — the object lands immediately but ReadyToUse
// flips true only after `zfs snapshot` completes (usually O(100ms)).
// PVC-from-snapshot binding handles the wait implicitly — the CSI
// controller blocks the PVC until the source snapshot is Ready — so
// callers don't need to poll themselves.
func Snapshot(ctx context.Context, masterSSH utils.SSHClient, names *utils.Names, dbName, label string) (string, error) {
	if masterSSH == nil {
		return "", fmt.Errorf("postgres.Snapshot: master ssh required")
	}
	if names == nil {
		return "", fmt.Errorf("postgres.Snapshot: names required")
	}
	if label == "" {
		label = time.Now().UTC().Format("20060102t150405z")
	}
	if err := utils.ValidateName("snapshot label", label); err != nil {
		return "", err
	}
	snapName := names.KubeDatabaseSnapshot(dbName, label)
	return snapName, applyVolumeSnapshot(ctx, masterSSH, names, dbName, snapName)
}

// applyVolumeSnapshot is the name-agnostic apply primitive both
// Snapshot (user-facing, label-derived name) and Branch (snapshot
// name derived from KubeDatabaseBranchSnapshot) compose onto. Doing
// the manifest-render + kubectl-apply in one place keeps the YAML
// template in a single location and stops callers from duplicating
// the EnsureSnapshotClass prerequisite.
func applyVolumeSnapshot(ctx context.Context, masterSSH utils.SSHClient, names *utils.Names, dbName, snapName string) error {
	if err := EnsureSnapshotClass(ctx, masterSSH); err != nil {
		return err
	}
	namespace := names.KubeNamespace()
	pvcName := names.KubeDatabasePVC(dbName)

	manifest := fmt.Sprintf(`apiVersion: %s
kind: VolumeSnapshot
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: nvoi
    nvoi/snapshot-of: %s
spec:
  volumeSnapshotClassName: %s
  source:
    persistentVolumeClaimName: %s
`, snapshotAPIVersion, snapName, namespace, pvcName, SnapshotClassName, pvcName)

	if err := applyManifest(ctx, masterSSH, manifest); err != nil {
		return fmt.Errorf("apply VolumeSnapshot %s: %w", snapName, err)
	}
	return nil
}

// ListSnapshots returns every VolumeSnapshot in the namespace that's
// tagged as belonging to the given DB. Names come back in kubectl's
// ordering (stable by creation timestamp).
func ListSnapshots(ctx context.Context, masterSSH utils.SSHClient, names *utils.Names, dbName string) ([]string, error) {
	namespace := names.KubeNamespace()
	pvcName := names.KubeDatabasePVC(dbName)
	cmd := fmt.Sprintf(
		`sudo k3s kubectl -n %s get volumesnapshot -l nvoi/snapshot-of=%s -o name 2>/dev/null || true`,
		namespace, pvcName,
	)
	out, err := masterSSH.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	var snapNames []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// `kubectl get -o name` emits `volumesnapshot/NAME`.
		if i := strings.LastIndex(line, "/"); i >= 0 {
			line = line[i+1:]
		}
		snapNames = append(snapNames, line)
	}
	return snapNames, nil
}

// DeleteSnapshot removes a VolumeSnapshot. The ZFS-LocalPV CSI runs
// `zfs destroy` on the underlying dataset (ReclaimPolicy=Delete on the
// SnapshotClass). Idempotent via --ignore-not-found. Snapshot name is
// validated as DNS-1123 so it can't inject into the kubectl command.
func DeleteSnapshot(ctx context.Context, masterSSH utils.SSHClient, namespace, snapshotName string) error {
	if err := utils.ValidateName("snapshot name", snapshotName); err != nil {
		return err
	}
	cmd := fmt.Sprintf(
		`sudo k3s kubectl -n %s delete volumesnapshot %s --ignore-not-found`,
		namespace, snapshotName,
	)
	if _, err := masterSSH.Run(ctx, cmd); err != nil {
		return fmt.Errorf("delete snapshot %s: %w", snapshotName, err)
	}
	return nil
}

// ── Branch primitives (typed Go structs via kube.Client.Apply) ───────────

// BranchSource is the subset of a DatabaseRequest the branch helper
// needs. Derived names (PVC, Service, credentials Secret) come from
// *utils.Names so the naming convention stays in one place. Image
// naming is resolved via postgres.ImageFor(Version) — callers pass the
// same Version string that cfg.databases.X.version carries, not a
// pre-rendered image tag.
type BranchSource struct {
	Names      *utils.Names // cluster naming — must be non-nil
	DBName     string       // databases.<name> key
	Size       int          // PVC storage request in GiB
	Version    string       // postgres version (empty → defaultPostgresVersion)
	ServerRole string       // nvoi-role label of the DB node (branch shares the node)
}

// BranchResult names the workloads the branch created so callers can
// report them or clean them up later.
type BranchResult struct {
	Name         string
	PVC          string
	Service      string
	SnapshotName string
}

// TODO(#68, ttl): extend BranchSource with a TTL field (time.Duration)
// and stamp `nvoi/branch-ttl=<RFC3339-expiry>` on every branch
// workload's labels from buildBranch*. A reconcile sweep (see TODO in
// internal/reconcile/databases.go) then garbage-collects branches past
// their expiry. Without it, per-PR preview branches accumulate until
// the zpool fills. Resurface when branch gets wired into a CI
// per-PR-preview workflow.

// Branch creates a sibling postgres StatefulSet bound to a clone of
// the source DB's data. Composes over Snapshot: take a fresh snapshot
// of the source PVC, create a new PVC with dataSource pointing at it,
// apply a sibling StatefulSet + Service pointing at the new PVC.
//
// Naming: branches live under `{source-pvc}-br-{branch}` so siblings
// don't collide with the primary or with each other. The branch's
// Service DNS is the same name — consumer tooling connects to it
// instead of the primary's Service.
//
// Idempotent on re-run: Apply reconciles each object. Callers wanting
// a fresh snapshot should Snapshot → Branch rather than re-running
// Branch against the same branch name.
//
// kc may be nil only in tests that stub the apply path; production
// callers always pass the master kube client.
func Branch(ctx context.Context, kc *kube.Client, masterSSH utils.SSHClient, src BranchSource, branchName string) (BranchResult, error) {
	if masterSSH == nil {
		return BranchResult{}, fmt.Errorf("postgres.Branch: master ssh required")
	}
	if kc == nil {
		return BranchResult{}, fmt.Errorf("postgres.Branch: kube client required")
	}
	if src.Names == nil {
		return BranchResult{}, fmt.Errorf("postgres.Branch: names required")
	}
	if err := utils.ValidateName("branch", branchName); err != nil {
		return BranchResult{}, err
	}
	if src.Size <= 0 {
		return BranchResult{}, fmt.Errorf("postgres.Branch: source size must be > 0")
	}

	// The branch workload name becomes a k8s Service name → DNS-1123
	// label (63-char cap). ValidateName on branchName alone isn't
	// enough because the final name is a composite of several
	// already-validated segments. Check the composite upfront so we
	// fail cleanly instead of mid-apply with an opaque kube error.
	branchWorkload := src.Names.KubeDatabaseBranch(src.DBName, branchName)
	if len(branchWorkload) > 63 {
		return BranchResult{}, fmt.Errorf(
			"postgres.Branch: composite name %q exceeds the 63-char DNS-1123 label limit for Service names — shorten the app/env/database/branch names",
			branchWorkload,
		)
	}
	namespace := src.Names.KubeNamespace()

	// 1. Snapshot the source PVC. Uses KubeDatabaseBranchSnapshot so
	// the snapshot's name encodes the branch lineage — list/delete by
	// prefix traces a branch back to its seed without hand-crafting
	// the "br-" segment at this call site.
	snapName := src.Names.KubeDatabaseBranchSnapshot(src.DBName, branchName)
	if err := applyVolumeSnapshot(ctx, masterSSH, src.Names, src.DBName, snapName); err != nil {
		return BranchResult{}, fmt.Errorf("snapshot source: %w", err)
	}

	// 2. PVC cloned from the snapshot (typed, applied via kube.Client).
	if err := kc.Apply(ctx, namespace, buildBranchPVC(src, branchWorkload, snapName)); err != nil {
		return BranchResult{}, fmt.Errorf("apply branch PVC: %w", err)
	}

	// 3. Sibling Service (typed).
	if err := kc.Apply(ctx, namespace, buildBranchService(src, branchWorkload)); err != nil {
		return BranchResult{}, fmt.Errorf("apply branch Service: %w", err)
	}

	// 4. Sibling StatefulSet (typed, bound to the clone PVC).
	if err := kc.Apply(ctx, namespace, buildBranchStatefulSet(src, branchWorkload)); err != nil {
		return BranchResult{}, fmt.Errorf("apply branch StatefulSet: %w", err)
	}

	return BranchResult{
		Name:         branchName,
		PVC:          branchWorkload,
		Service:      branchWorkload,
		SnapshotName: snapName,
	}, nil
}

// TODO(#68, rollback): add Rollback(ctx, kc, masterSSH, namespace,
// sourcePVC, snapshotName) that performs an in-place restore of the
// PRIMARY DB from one of its snapshots. Shape:
//
//   1. Scale down the source StatefulSet to 0 (release the PVC).
//   2. Delete the source PVC.
//   3. Recreate the source PVC with dataSource pointing at snapshotName
//      (same buildPVC shape, just with a DataSource added).
//   4. Scale back up.
//
// Different from Branch because it swaps the PRIMARY's PVC — same
// Service, same DSN, clients don't reconfigure — whereas Branch creates
// a sibling. Target downtime: O(seconds) for the pod restart + zfs
// clone. Today the same outcome is reachable via restore --from-backup
// (slower, dump+restore) or branch-delete+branch (wrong semantics —
// creates a sibling). Resurface when a concrete "jump backward 15 min"
// recovery scenario lands.

// DeleteBranch removes every workload created for a branch — the
// sibling StatefulSet, Service, PVC, and the VolumeSnapshot the PVC
// was cloned from. ReclaimPolicy=Delete on both the StorageClass and
// the SnapshotClass means the underlying ZFS datasets are destroyed
// too. Idempotent (kc.DeleteByName + DeletePVC swallow NotFound;
// kubectl delete --ignore-not-found for the snapshot).
func DeleteBranch(ctx context.Context, kc *kube.Client, masterSSH utils.SSHClient, names *utils.Names, dbName, branchName string) error {
	if kc == nil {
		return fmt.Errorf("postgres.DeleteBranch: kube client required")
	}
	if names == nil {
		return fmt.Errorf("postgres.DeleteBranch: names required")
	}
	if err := utils.ValidateName("branch", branchName); err != nil {
		return err
	}
	namespace := names.KubeNamespace()
	branchWorkload := names.KubeDatabaseBranch(dbName, branchName)

	// StatefulSet + Service first — evicts the pod before its PVC goes.
	if err := kc.DeleteByName(ctx, namespace, branchWorkload); err != nil {
		return fmt.Errorf("delete branch workloads: %w", err)
	}
	// PVC next — CSI runs zfs destroy on the clone dataset.
	if err := kc.DeletePVC(ctx, namespace, branchWorkload); err != nil {
		return fmt.Errorf("delete branch pvc: %w", err)
	}
	// VolumeSnapshot last — CSI runs zfs destroy on the snapshot. Via
	// kubectl because VolumeSnapshot isn't in our typed surface.
	snapName := names.KubeDatabaseBranchSnapshot(dbName, branchName)
	if masterSSH != nil {
		if err := DeleteSnapshot(ctx, masterSSH, namespace, snapName); err != nil {
			return err
		}
	}
	return nil
}

// ── Typed builders ───────────────────────────────────────────────────────

func buildBranchPVC(src BranchSource, name, snapshotName string) *corev1.PersistentVolumeClaim {
	sc := ZFSStorageClassName
	snapshotAPI := "snapshot.storage.k8s.io"
	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: src.Names.KubeNamespace(),
			Labels:    branchLabels(src),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &sc,
			DataSource: &corev1.TypedLocalObjectReference{
				APIGroup: &snapshotAPI,
				Kind:     "VolumeSnapshot",
				Name:     snapshotName,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", src.Size)),
				},
			},
		},
	}
}

func buildBranchService(src BranchSource, name string) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: src.Names.KubeNamespace(),
			Labels:    branchLabels(src),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{utils.LabelAppName: name},
			Ports: []corev1.ServicePort{{
				Name:       "postgres",
				Port:       5432,
				TargetPort: intstr.FromInt(5432),
			}},
		},
	}
}

func buildBranchStatefulSet(src BranchSource, name string) *appsv1.StatefulSet {
	replicas := int32(1)
	credsSecret := src.Names.KubeDatabaseCredentials(src.DBName)
	secretKeyRef := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: credsSecret},
				Key:                  key,
			},
		}
	}
	podLabels := map[string]string{utils.LabelAppName: name}
	for k, v := range branchLabels(src) {
		podLabels[k] = v
	}
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: src.Names.KubeNamespace(),
			Labels:    branchLabels(src),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{utils.LabelAppName: name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					// Branch shares the source node — the ZFS clone
					// lives in the same pool. Cross-node branching
					// isn't supported (pools are node-local).
					NodeSelector: map[string]string{utils.LabelNvoiRole: src.ServerRole},
					Containers: []corev1.Container{{
						Name:  "postgres",
						Image: ImageFor(src.Version),
						Env: []corev1.EnvVar{
							// Credentials come from the source DB's
							// Secret — ZFS clones carry pg_roles bit-
							// exact so auth works identically.
							{Name: "POSTGRES_USER", ValueFrom: secretKeyRef("user")},
							{Name: "POSTGRES_PASSWORD", ValueFrom: secretKeyRef("password")},
							{Name: "POSTGRES_DB", ValueFrom: secretKeyRef("database")},
						},
						Ports: []corev1.ContainerPort{{ContainerPort: 5432, Name: "postgres"}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							MountPath: "/var/lib/postgresql/data",
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: name},
						},
					}},
				},
			},
		},
	}
}

// branchLabels is the common label set stamped on every workload a
// branch creates. brLabel ties siblings back to the source DB so
// list/delete operations can scope cleanly; managed-by is the usual
// nvoi-owner selector.
func branchLabels(src BranchSource) map[string]string {
	return map[string]string{
		utils.LabelAppManagedBy: utils.LabelManagedBy,
		brLabel:                 src.Names.KubeDatabasePVC(src.DBName),
	}
}

// applyManifest pipes a YAML manifest into `kubectl apply -f -` via
// SSH. Used only for kinds outside core client-go (VolumeSnapshot,
// VolumeSnapshotClass) — typed kinds go through kube.Client.Apply
// like the rest of the codebase.
func applyManifest(ctx context.Context, masterSSH utils.SSHClient, manifest string) error {
	cmd := fmt.Sprintf(
		"sudo k3s kubectl apply -f - <<'NVOI_EOF'\n%s\nNVOI_EOF\n",
		strings.TrimSpace(manifest),
	)
	if _, err := masterSSH.Run(ctx, cmd); err != nil {
		return fmt.Errorf("kubectl apply: %w", err)
	}
	return nil
}
