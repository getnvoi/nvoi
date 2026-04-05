package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	return &Config{
		Servers: map[string]Server{
			"master": {Type: "cx23", Region: "fsn1"},
		},
		Services: map[string]Service{
			"web": {Image: "nginx", Port: 80},
		},
	}
}

func TestValidate_ValidMinimal(t *testing.T) {
	errs := Validate(validConfig())
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidate_NoServers(t *testing.T) {
	cfg := validConfig()
	cfg.Servers = nil
	errs := Validate(cfg)
	assertHasError(t, errs, "at least one server")
}

func TestValidate_ServerMissingType(t *testing.T) {
	cfg := validConfig()
	cfg.Servers["master"] = Server{Region: "fsn1"}
	errs := Validate(cfg)
	assertHasError(t, errs, "servers.master.type")
}

func TestValidate_ServerMissingRegion(t *testing.T) {
	cfg := validConfig()
	cfg.Servers["master"] = Server{Type: "cx23"}
	errs := Validate(cfg)
	assertHasError(t, errs, "servers.master.region")
}

func TestValidate_NoServices(t *testing.T) {
	cfg := validConfig()
	cfg.Services = nil
	errs := Validate(cfg)
	assertHasError(t, errs, "at least one service")
}

func TestValidate_ServiceNoImageNoBuild(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Port: 80}
	errs := Validate(cfg)
	assertHasError(t, errs, "must have either image or build")
}

func TestValidate_ServiceBothImageAndBuild(t *testing.T) {
	cfg := validConfig()
	cfg.Build = map[string]Build{"web": {Source: "org/repo"}}
	cfg.Services["web"] = Service{Image: "nginx", Build: "web", Port: 80}
	errs := Validate(cfg)
	assertHasError(t, errs, "cannot have both image and build")
}

func TestValidate_ServiceBuildRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Build: "nonexistent", Port: 80}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined build target")
}

func TestValidate_ServiceServerRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Server: "nonexistent"}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined server")
}

func TestValidate_ServiceStorageRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Storage: []string{"nonexistent"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined storage")
}

func TestValidate_ServiceVolumeRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Volumes: []string{"pgdata:/data"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "volume \"pgdata\" is not defined")
}

func TestValidate_ServiceVolumeBadFormat(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Volumes: []string{"nopath"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "must be name:/path")
}

func TestValidate_ServiceVolumeAbsolutePathOK(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Volumes: []string{"/host/path:/container/path"}}
	errs := Validate(cfg)
	if len(errs) > 0 {
		t.Errorf("absolute path volume should be valid, got: %v", errs)
	}
}

func TestValidate_VolumeServerRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Volumes = map[string]Volume{"pgdata": {Size: 30, Server: "nonexistent"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined server")
}

func TestValidate_VolumeSizeZero(t *testing.T) {
	cfg := validConfig()
	cfg.Volumes = map[string]Volume{"pgdata": {Size: 0, Server: "master"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "must be > 0")
}

func TestValidate_BuildMissingSource(t *testing.T) {
	cfg := validConfig()
	cfg.Build = map[string]Build{"web": {Source: ""}}
	errs := Validate(cfg)
	assertHasError(t, errs, "build.web.source")
}

func TestValidate_DomainServiceMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Domains = map[string]Domains{"api": {"api.example.com"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined service")
}

func TestValidate_DomainServiceNoPort(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx"} // no port
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "has no port")
}

func TestValidate_DomainEmpty(t *testing.T) {
	cfg := validConfig()
	cfg.Domains = map[string]Domains{"web": {}}
	errs := Validate(cfg)
	assertHasError(t, errs, "at least one domain")
}

func TestValidate_FullConfig(t *testing.T) {
	cfg := &Config{
		Servers: map[string]Server{
			"master":   {Type: "cx23", Region: "fsn1"},
			"worker-1": {Type: "cx33", Region: "fsn1"},
		},
		Volumes: map[string]Volume{
			"pgdata":    {Size: 30, Server: "master"},
			"meili-data": {Size: 20, Server: "master"},
		},
		Build: map[string]Build{
			"web": {Source: "benbonnet/dummy-rails"},
		},
		Storage: map[string]Storage{
			"assets": {CORS: true},
		},
		Services: map[string]Service{
			"db":          {Image: "postgres:17", Volumes: []string{"pgdata:/var/lib/postgresql/data"}, Secrets: []string{"POSTGRES_PASSWORD"}},
			"meilisearch": {Image: "getmeili/meilisearch:latest", Volumes: []string{"meili-data:/meili_data"}},
			"web":         {Build: "web", Port: 80, Replicas: 2, Health: "/up", Server: "worker-1", Storage: []string{"assets"}},
			"jobs":        {Build: "web", Command: "bin/jobs", Server: "worker-1"},
		},
		Domains: map[string]Domains{
			"web": {"final.nvoi.to"},
		},
	}
	errs := Validate(cfg)
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := &Config{
		Servers:  map[string]Server{},
		Services: map[string]Service{},
	}
	errs := Validate(cfg)
	if len(errs) < 2 {
		t.Errorf("expected multiple errors, got %d: %v", len(errs), errs)
	}
}

func assertHasError(t *testing.T, errs []error, substr string) {
	t.Helper()
	for _, err := range errs {
		if strings.Contains(err.Error(), substr) {
			return
		}
	}
	t.Errorf("expected error containing %q, got: %v", substr, errs)
}
