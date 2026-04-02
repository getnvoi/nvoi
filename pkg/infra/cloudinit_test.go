package infra

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderCloudInit(t *testing.T) {
	fakeKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest test@test"

	output, err := RenderCloudInit(fakeKey)
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
}
