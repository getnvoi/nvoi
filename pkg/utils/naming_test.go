package utils

import (
	"strings"
	"testing"
)

func TestNewNames(t *testing.T) {
	tests := []struct {
		name    string
		app     string
		env     string
		wantErr bool
	}{
		{"valid inputs", "dummy-rails", "production", false},
		{"empty app", "", "production", true},
		{"empty env", "dummy-rails", "", true},
		{"both empty", "", "", true},
		{"single char each", "a", "b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := NewNames(tt.app, tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n == nil {
				t.Fatal("expected non-nil Names")
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercase", "MyApp", "myapp"},
		{"spaces to dash", "my app", "my-app"},
		{"underscores to dash", "my_app_name", "my-app-name"},
		{"multiple non-alphanum collapsed", "my!!!app", "my-app"},
		{"leading non-alphanum trimmed", "---leading", "leading"},
		{"trailing non-alphanum trimmed", "trailing---", "trailing"},
		{"both ends trimmed", "---both---", "both"},
		{"mixed special chars", "My.App_V2!!", "my-app-v2"},
		{"already clean", "clean", "clean"},
		{"digits preserved", "app123", "app123"},
		{"63 char truncation", strings.Repeat("a", 100), strings.Repeat("a", 63)},
		{"exactly 63 unchanged", strings.Repeat("b", 63), strings.Repeat("b", 63)},
		{"under 63 unchanged", strings.Repeat("c", 62), strings.Repeat("c", 62)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitize(tt.input)
			if got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNamesInfrastructure(t *testing.T) {
	n, err := NewNames("dummy-rails", "production")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Base", n.Base(), "nvoi-dummy-rails-production"},
		{"Firewall", n.Firewall(), "nvoi-dummy-rails-production-fw"},
		{"Network", n.Network(), "nvoi-dummy-rails-production-net"},
		{"Server master", n.Server("master"), "nvoi-dummy-rails-production-master"},
		{"Server worker1", n.Server("worker1"), "nvoi-dummy-rails-production-worker1"},
		{"Volume pgdata", n.Volume("pgdata"), "nvoi-dummy-rails-production-pgdata"},
		{"Bucket assets", n.Bucket("assets"), "nvoi-dummy-rails-production-assets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestNamesKube(t *testing.T) {
	n, err := NewNames("dummy-rails", "production")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"KubeNamespace", n.KubeNamespace(), "nvoi-dummy-rails-production"},
		{"KubeSecrets", n.KubeSecrets(), "secrets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestNamesLabels(t *testing.T) {
	n, err := NewNames("dummy-rails", "production")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	labels := n.Labels()

	expected := map[string]string{
		"managed-by": "nvoi",
		"app":        "nvoi-dummy-rails-production",
		"env":        "production",
	}

	if len(labels) != len(expected) {
		t.Fatalf("labels has %d entries, want %d", len(labels), len(expected))
	}

	for k, want := range expected {
		got, ok := labels[k]
		if !ok {
			t.Errorf("missing label key %q", k)
			continue
		}
		if got != want {
			t.Errorf("labels[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestNamesPaths(t *testing.T) {
	n, err := NewNames("dummy-rails", "production")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"VolumeMountPath", n.VolumeMountPath("pgdata"), "/mnt/data/nvoi-dummy-rails-production-pgdata"},
		{"NamedVolumeHostPath", n.NamedVolumeHostPath("pgdata"), "/var/lib/nvoi/volumes/nvoi-dummy-rails-production/pgdata"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestParseVolumeMount(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSource string
		wantTarget string
		wantNamed  bool
		wantOK     bool
	}{
		{
			name:       "named volume",
			input:      "pgdata:/var/lib/postgresql/data",
			wantSource: "pgdata",
			wantTarget: "/var/lib/postgresql/data",
			wantNamed:  true,
			wantOK:     true,
		},
		{
			name:       "bind mount absolute",
			input:      "/host/path:/container/path",
			wantSource: "/host/path",
			wantTarget: "/container/path",
			wantNamed:  false,
			wantOK:     true,
		},
		{
			name:       "bind mount relative dot",
			input:      "./local:/container/path",
			wantSource: "./local",
			wantTarget: "/container/path",
			wantNamed:  false,
			wantOK:     true,
		},
		{
			name:       "three parts colon separated",
			input:      "pgdata:/var/lib/postgresql/data:rw",
			wantSource: "pgdata",
			wantTarget: "/var/lib/postgresql/data",
			wantNamed:  true,
			wantOK:     true,
		},
		{
			name:   "missing colon",
			input:  "nodelimiter",
			wantOK: false,
		},
		{
			name:   "empty source",
			input:  ":/var/lib/data",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, target, named, ok := ParseVolumeMount(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
			if target != tt.wantTarget {
				t.Errorf("target = %q, want %q", target, tt.wantTarget)
			}
			if named != tt.wantNamed {
				t.Errorf("named = %v, want %v", named, tt.wantNamed)
			}
		})
	}
}

func TestStorageEnvPrefix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "assets", "STORAGE_ASSETS"},
		{"with dash", "my-bucket", "STORAGE_MY_BUCKET"},
		{"already upper", "UPLOADS", "STORAGE_UPLOADS"},
		{"multiple dashes", "a-b-c", "STORAGE_A_B_C"},
		{"lowercase with dash", "user-avatars", "STORAGE_USER_AVATARS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StorageEnvPrefix(tt.input)
			if got != tt.want {
				t.Errorf("StorageEnvPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
