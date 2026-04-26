package utils

// Version is the nvoi CLI's release tag. Overridden at build time by:
//
//	-ldflags "-X github.com/getnvoi/nvoi/pkg/utils.Version=v1.2.3"
//
// set in .github/workflows/release.yml on every `v*` tag. The default
// "latest" makes local/dev builds (bin/nvoi) pull
// docker.io/nvoi/db:latest — which release.yml publishes on every
// tagged release alongside the version-pinned tag. Tagged releases
// still inject vX.Y.Z so prod stays in lockstep with the binary.
// Local deploys "just work" without ldflags juggling.
var Version = "latest"
