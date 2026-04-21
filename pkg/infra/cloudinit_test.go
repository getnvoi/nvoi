package infra

import (
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/pkg/utils"
	"gopkg.in/yaml.v3"
)

func TestRenderCloudInit(t *testing.T) {
	fakeKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest test@test"

	output, err := RenderCloudInit(fakeKey, "nvoi-test-master")
	if err != nil {
		t.Fatalf("RenderCloudInit returned error: %v", err)
	}

	t.Run("starts with cloud-config header", func(t *testing.T) {
		if !strings.HasPrefix(output, "#cloud-config\n") {
			t.Errorf("output should start with #cloud-config, got: %q", output[:40])
		}
	})

	t.Run("contains the SSH public key", func(t *testing.T) {
		if !strings.Contains(output, fakeKey) {
			t.Errorf("output should contain the SSH public key %q", fakeKey)
		}
	})

	t.Run("is valid YAML", func(t *testing.T) {
		// Strip the #cloud-config header before unmarshalling
		yamlBody := strings.TrimPrefix(output, "#cloud-config\n")
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlBody), &parsed); err != nil {
			t.Fatalf("output is not valid YAML: %v", err)
		}
		if parsed == nil {
			t.Fatal("parsed YAML is nil")
		}
	})

	t.Run("contains user deploy", func(t *testing.T) {
		yamlBody := strings.TrimPrefix(output, "#cloud-config\n")
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlBody), &parsed); err != nil {
			t.Fatalf("output is not valid YAML: %v", err)
		}

		users, ok := parsed["users"]
		if !ok {
			t.Fatal("YAML missing 'users' key")
		}

		userList, ok := users.([]any)
		if !ok {
			t.Fatalf("users is not a list, got %T", users)
		}

		found := false
		for _, u := range userList {
			userMap, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := userMap["name"].(string); ok && name == "deploy" {
				found = true
				break
			}
		}
		if !found {
			t.Error("no user named 'deploy' found in cloud-config users")
		}
	})

	t.Run("sets hostname", func(t *testing.T) {
		yamlBody := strings.TrimPrefix(output, "#cloud-config\n")
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlBody), &parsed); err != nil {
			t.Fatalf("output is not valid YAML: %v", err)
		}
		hostname, ok := parsed["hostname"].(string)
		if !ok || hostname != "nvoi-test-master" {
			t.Errorf("hostname = %q, want %q", hostname, "nvoi-test-master")
		}
	})
}

// TestRenderBuilderCloudInit locks the shape of the role: builder cloud-init:
// Docker CE install, no k3s, data-root pointing at the cache mount, auto-start
// disabled so the provider can interpose the mount. Each assertion is a
// separate subtest so a regression failure points at exactly which bit of
// the contract broke.
func TestRenderBuilderCloudInit(t *testing.T) {
	fakeKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest test@test"
	hostname := "nvoi-test-build"

	output, err := RenderBuilderCloudInit(fakeKey, hostname)
	if err != nil {
		t.Fatalf("RenderBuilderCloudInit: %v", err)
	}

	t.Run("starts with cloud-config header", func(t *testing.T) {
		if !strings.HasPrefix(output, "#cloud-config\n") {
			t.Errorf("missing #cloud-config header; got prefix %q", output[:min(40, len(output))])
		}
	})

	t.Run("is valid YAML", func(t *testing.T) {
		yamlBody := strings.TrimPrefix(output, "#cloud-config\n")
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(yamlBody), &parsed); err != nil {
			t.Fatalf("not valid YAML: %v", err)
		}
	})

	t.Run("sets hostname", func(t *testing.T) {
		if !strings.Contains(output, "hostname: "+hostname) {
			t.Errorf("hostname line missing for %q", hostname)
		}
	})

	t.Run("contains the SSH public key", func(t *testing.T) {
		if !strings.Contains(output, fakeKey) {
			t.Error("ssh key not embedded")
		}
	})

	// Docker CE must be installed — this is the load-bearing promise of a
	// builder. Install line style may vary (version, flags) but the package
	// name + buildx plugin presence are the contract.
	t.Run("installs docker CE", func(t *testing.T) {
		if !strings.Contains(output, "docker-ce") {
			t.Error("docker-ce install command missing")
		}
		if !strings.Contains(output, "docker-buildx-plugin") {
			t.Error("docker-buildx-plugin install missing — buildx is mandatory for multi-arch builds")
		}
	})

	// No k3s! The builder is NOT a k8s node. A stray k3s install would make
	// the builder try to join the cluster and waste resources.
	t.Run("does not install k3s", func(t *testing.T) {
		if strings.Contains(output, "k3s") {
			t.Error("k3s must not appear in builder cloud-init — builders are not k8s nodes")
		}
	})

	// Docker data-root must point at the cache mount so buildkit layer cache
	// lands on the persistent volume, not the root disk.
	t.Run("docker data-root points at cache mount", func(t *testing.T) {
		if !strings.Contains(output, utils.BuilderCacheMountPath) {
			t.Errorf("BuilderCacheMountPath %q not referenced", utils.BuilderCacheMountPath)
		}
		if !strings.Contains(output, `"data-root":`) {
			t.Error("daemon.json data-root key missing — cache would land on root disk")
		}
	})

	// Docker must be DISABLED on boot — the provider's post-mount step enables
	// it. If we left it enabled, Docker would create /var/lib/nvoi/builder-cache
	// on the root disk before the volume is mounted, shadowing the volume.
	t.Run("disables docker auto-start", func(t *testing.T) {
		if !strings.Contains(output, "systemctl disable docker") {
			t.Error("`systemctl disable docker` missing — docker would auto-start before cache mount")
		}
	})

	// xfsprogs needed so the provider's post-mount blkid→mkfs.xfs step has
	// mkfs.xfs available.
	t.Run("installs xfsprogs for cache volume formatting", func(t *testing.T) {
		if !strings.Contains(output, "xfsprogs") {
			t.Error("xfsprogs not in packages — mkfs.xfs would fail in post-mount step")
		}
	})

	// Operator user joins the docker group so the SSH BuildProvider can
	// run `docker buildx` without sudo over the dispatch SSH session.
	t.Run("adds operator user to docker group", func(t *testing.T) {
		want := "usermod -aG docker " + utils.DefaultUser
		if !strings.Contains(output, want) {
			t.Errorf("missing %q — SSH BuildProvider would need sudo for docker", want)
		}
	})

	// git is load-bearing for the SSH BuildProvider. It clones
	// BuildRequest.GitRemote at BuildRequest.GitRef into a per-build
	// workspace on the builder, then buildx points at that workspace.
	// A fresh builder without git is a silent `command not found` on
	// every Build call, so the assertion is on the packages list, not
	// on the (environment-dependent) git binary path.
	t.Run("installs git for source clone", func(t *testing.T) {
		if !strings.Contains(output, "- git") && !strings.Contains(output, "git\n") {
			t.Error("git not in packages — SSH BuildProvider clone would fail with command-not-found")
		}
	})

	// The builder is NOT an nvoi runtime. An earlier design iteration
	// dispatched `nvoi deploy --local` to the builder (requiring the
	// binary on PATH); the PR-B SSH BuildProvider runs docker directly,
	// no nvoi needed. Assertion on both the installer URL and the
	// dispatch CLI so either regression is caught.
	t.Run("does not install nvoi runtime", func(t *testing.T) {
		for _, forbidden := range []string{"get.nvoi.to", "nvoi deploy --local"} {
			if strings.Contains(output, forbidden) {
				t.Errorf("builder cloud-init must not reference %q — PR-B dispatches docker directly", forbidden)
			}
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
