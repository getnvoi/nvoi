package reconcile

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
)

// ResolveImage returns the full image reference for a service, respecting
// the user's declared image + build flag + the per-deploy hash.
//
// Resolution rules (Kamal-style, adapted to nvoi's multi-registry map):
//
//  1. If svc.Build is nil/empty → return svc.Image verbatim. Pull-only
//     services are never rewritten — a literal `image: postgres:17` ships
//     as `postgres:17` to the PodSpec, kubelet pulls from Docker Hub.
//
//  2. If svc.Build is set → the image ref goes through host + tag
//     resolution so the deploy always produces a unique, registry-bound
//     reference:
//
//     a. HOST. If the YAML image has a registry host prefix (ghcr.io/…,
//     docker.io/…, localhost:5000/…) and that host is in cfg.Registry,
//     we keep it. If it's missing from cfg.Registry, that's an error
//     (ValidateConfig already catches it — this is belt-and-suspenders).
//     If the YAML image has NO host prefix, we pick the single
//     registry declared. More than one declared + no host prefix →
//     ambiguous → error.
//
//     b. TAG. If the YAML image already carries a tag (`repo:v2`), we
//     keep it and append `-<hash>` so the pinned version is visible
//     AND every deploy gets a unique ref. If no tag, we use the
//     hash alone (`repo:<hash>`). Either way the final PodSpec.image
//     changes every deploy → automatic rolling restart.
//
// Examples (hash = "20260417-143022"):
//
//	build+one registry (docker.io)+"deemx/nvoi-api"
//	  → "docker.io/deemx/nvoi-api:20260417-143022"
//
//	build+one registry+"deemx/nvoi-api:v2"
//	  → "docker.io/deemx/nvoi-api:v2-20260417-143022"
//
//	build+"ghcr.io/org/api:v2" (host explicit)
//	  → "ghcr.io/org/api:v2-20260417-143022"
//
//	no build+"postgres:17"
//	  → "postgres:17"
func ResolveImage(cfg *config.AppConfig, svcName string, hash string) (string, error) {
	svc, ok := cfg.Services[svcName]
	if !ok {
		return "", fmt.Errorf("services.%s: not defined", svcName)
	}
	if svc.Build == nil || svc.Build.Context == "" {
		return svc.Image, nil
	}

	img := svc.Image
	if img == "" {
		return "", fmt.Errorf("services.%s.build: image is required", svcName)
	}

	// ── HOST ──────────────────────────────────────────────────────────
	if h := imageRegistryHost(img); h != "" {
		if _, ok := cfg.Registry[h]; !ok {
			return "", fmt.Errorf("services.%s.build: image targets registry %q but no `registry.%s` entry", svcName, h, h)
		}
	} else {
		switch len(cfg.Registry) {
		case 0:
			return "", fmt.Errorf("services.%s.build: set but no `registry:` block — nvoi needs push credentials", svcName)
		case 1:
			var host string
			for h := range cfg.Registry {
				host = h
			}
			img = host + "/" + img
		default:
			return "", fmt.Errorf("services.%s.image: %q has no host prefix but multiple registries are declared — write a fully qualified tag like `<host>/%s` to disambiguate", svcName, img, img)
		}
	}

	// ── TAG ───────────────────────────────────────────────────────────
	if hash == "" {
		return "", fmt.Errorf("services.%s.build: empty deploy hash — reconcile.Deploy must set dc.Cluster.DeployHash before calling ResolveImage", svcName)
	}
	// Digest-pinned references (`repo@sha256:…`) are content-addressed.
	// Appending `:<hash>` would produce garbage like `repo@sha256:xxx:yyy`
	// that docker rejects. Leave as-is — mixing `build:` with a digest
	// pin is a user footgun we pass through rather than rewrite.
	if strings.Contains(img, "@sha256:") {
		return img, nil
	}
	if t := imageTag(img); t != "" {
		return img + "-" + hash, nil
	}
	return img + ":" + hash, nil
}

// imageTag returns the tag portion of an image reference (the part after
// the LAST ':' that isn't part of the host:port prefix), or "" if there
// is no tag.
//
//	"foo"                     → ""
//	"foo:v1"                  → "v1"
//	"ghcr.io/org/foo"         → ""
//	"ghcr.io/org/foo:v1"      → "v1"
//	"localhost:5000/foo"      → ""  (the 5000 is a port, not a tag)
//	"localhost:5000/foo:v1"   → "v1"
//	"repo@sha256:abc..."      → ""  (digest — not a tag, we don't suffix digests)
func imageTag(image string) string {
	// Digest form — we never suffix sha256 references. The user pinned
	// a content hash; respect it.
	if strings.Contains(image, "@sha256:") {
		return ""
	}
	// Work only on the portion after the last slash (the repo:tag piece);
	// anything before belongs to the host:port/path prefix.
	lastSlash := strings.LastIndexByte(image, '/')
	piece := image
	if lastSlash >= 0 {
		piece = image[lastSlash+1:]
	}
	colon := strings.IndexByte(piece, ':')
	if colon < 0 {
		return ""
	}
	return piece[colon+1:]
}
