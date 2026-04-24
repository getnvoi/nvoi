package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// ZFS-backed branching primitives.
//
// Every branching op is a thin wrapper over k8s-standard VolumeSnapshot
// + PVC.dataSource APIs — OpenEBS ZFS-LocalPV's CSI driver responds to
// these by running `zfs snapshot` / `zfs clone` internally. Because
// those kinds (snapshot.storage.k8s.io/v1) aren't in the core client-
// go, we apply/query them via `sudo k3s kubectl` on the master shell,
// same pattern as EnsureZFSCSI. Keeps nvoi core free of the
// external-snapshotter client surface for a feature with a narrow
// lifecycle footprint.

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

// EnsureSnapshotClass installs the VolumeSnapshotClass via kubectl on
// master. Idempotent. Called by branching ops before they create a
// VolumeSnapshot — without the class existing, the snapshot request
// would fail.
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
parameters:
  snapshotNamespace: openebs
`, snapshotAPIVersion, SnapshotClassName)
	return applyManifest(ctx, masterSSH, manifest)
}

// Snapshot creates a VolumeSnapshot of the DB's PVC. Label is optional
// metadata; when empty a timestamp-based name is used. Returns the
// snapshot's name so callers can later reference it for clone/rollback.
//
// Concurrency note: VolumeSnapshot creation is async from the CSI
// controller's standpoint — the object lands immediately but
// ReadyToUse flips true only after `zfs snapshot` completes (usually
// O(100ms) for the pool sizes we're building). Callers that need the
// snapshot to be ready before proceeding (branch, rollback) poll for
// readiness via WaitSnapshotReady.
func Snapshot(ctx context.Context, masterSSH utils.SSHClient, namespace, pvcName, label string) (string, error) {
	if masterSSH == nil {
		return "", fmt.Errorf("postgres.Snapshot: master ssh required")
	}
	if label == "" {
		label = time.Now().UTC().Format("20060102t150405z")
	}
	snapName := fmt.Sprintf("%s-%s", pvcName, label)

	if err := EnsureSnapshotClass(ctx, masterSSH); err != nil {
		return "", err
	}

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
		return "", fmt.Errorf("apply VolumeSnapshot %s: %w", snapName, err)
	}
	return snapName, nil
}

// ListSnapshots returns every VolumeSnapshot in the namespace that's
// tagged as belonging to the given DB. Names are returned in the order
// kubectl produces them (stable by creation timestamp on the CSI side).
func ListSnapshots(ctx context.Context, masterSSH utils.SSHClient, namespace, pvcName string) ([]string, error) {
	cmd := fmt.Sprintf(
		`sudo k3s kubectl -n %s get volumesnapshot -l nvoi/snapshot-of=%s -o name 2>/dev/null || true`,
		namespace, pvcName,
	)
	out, err := masterSSH.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// `kubectl get -o name` emits `volumesnapshot/NAME`.
		if i := strings.LastIndex(line, "/"); i >= 0 {
			line = line[i+1:]
		}
		names = append(names, line)
	}
	return names, nil
}

// DeleteSnapshot removes a VolumeSnapshot. The ZFS-LocalPV CSI
// responds by running `zfs destroy` on the underlying dataset
// (ReclaimPolicy=Delete on the SnapshotClass). Idempotent — NotFound
// is swallowed by kubectl when `--ignore-not-found` is passed.
func DeleteSnapshot(ctx context.Context, masterSSH utils.SSHClient, namespace, snapshotName string) error {
	cmd := fmt.Sprintf(
		`sudo k3s kubectl -n %s delete volumesnapshot %s --ignore-not-found`,
		namespace, snapshotName,
	)
	if _, err := masterSSH.Run(ctx, cmd); err != nil {
		return fmt.Errorf("delete snapshot %s: %w", snapshotName, err)
	}
	return nil
}

// applyManifest pipes a YAML manifest into `kubectl apply -f -` via
// SSH. The `cat <<'EOF' | kubectl apply -f -` pattern is safer than
// stringifying into argv because YAML content routinely contains
// shell-reserved characters.
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
