package infra

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSwapSize(t *testing.T) {
	tests := []struct {
		disk int
		want int
	}{
		{0, 1024},   // default 20GB → 1024MB
		{5, 512},    // floor
		{10, 512},   // 512MB
		{20, 1024},  // 1GB
		{40, 2048},  // 2GB
		{100, 2048}, // cap
	}
	for _, tt := range tests {
		got := SwapSize(tt.disk)
		if got != tt.want {
			t.Errorf("SwapSize(%d) = %d, want %d", tt.disk, got, tt.want)
		}
	}
}

func TestRenderCloudInit(t *testing.T) {
	fakeKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest test@test"

	output, err := RenderCloudInit(fakeKey, "nvoi-test-master", 20)
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

	t.Run("contains swap setup", func(t *testing.T) {
		if !strings.Contains(output, "swapfile") {
			t.Error("output should contain swap setup")
		}
		if !strings.Contains(output, "mkswap") {
			t.Error("output should contain mkswap")
		}
		if !strings.Contains(output, "swapon") {
			t.Error("output should contain swapon")
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
