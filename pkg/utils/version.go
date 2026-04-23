package utils

// Version is the nvoi CLI's release tag. Overridden at build time by:
//
//	-ldflags "-X github.com/getnvoi/nvoi/pkg/utils.Version=v1.2.3"
//
// set in .github/workflows/release.yml on every `v*` tag. The default
// "main" is what unreleased/dev builds ship with — anything that pins
// container images by Version (today: docker.io/nvoi/backup:<Version>)
// must tolerate a "main" tag locally, or the caller can override before
// deploy.
var Version = "main"
