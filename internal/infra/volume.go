package infra

// TODO Phase 3: Port from nvoi-platform/internal/infra/volume.go (~229 lines)
//   - ensureAndMountVolume(ctx, prov, volumeName, location, srv, mountPath, sizeGB, privKey, labels, w)
//   - resolveDevicePath(ctx, prov, vol, ssh)
//   - mountVolumeOnHost(ctx, ssh, devicePath, mountPath)
//   - waitForDevice(ctx, ssh, devicePath)
//
// Key change: called per-volume from cmd/volume.go, not batch from ops layer.
