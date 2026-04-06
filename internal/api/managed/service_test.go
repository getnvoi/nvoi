package managed

import (
	"sort"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/api/config"
)

// ── Registry ───────────────────────────────────────────────────────────────────

func TestRegistry_AllRegistered(t *testing.T) {
	for _, kind := range []string{"postgres", "redis", "meilisearch"} {
		ms, ok := Get(kind)
		if !ok {
			t.Errorf("%s not registered", kind)
			continue
		}
		if ms.Kind() != kind {
			t.Errorf("%s.Kind() = %q", kind, ms.Kind())
		}
	}
}

func TestRegistry_Unknown(t *testing.T) {
	_, ok := Get("mongodb")
	if ok {
		t.Error("mongodb should not be registered")
	}
}

// ── Postgres ───────────────────────────────────────────────────────────────────

func TestPostgres_Spec(t *testing.T) {
	ms, _ := Get("postgres")
	spec := ms.Spec("db")

	if spec.Image != "postgres:17" {
		t.Errorf("image = %q", spec.Image)
	}
	if spec.Port != 5432 {
		t.Errorf("port = %d", spec.Port)
	}
	if len(spec.Volumes) != 1 || spec.Volumes[0] != "db-data:/var/lib/postgresql/data" {
		t.Errorf("volumes = %v", spec.Volumes)
	}
	assertContains(t, spec.Env, "POSTGRES_USER=postgres")
	assertContains(t, spec.Env, "POSTGRES_DB=db")
	assertContains(t, spec.Secrets, "POSTGRES_PASSWORD=POSTGRES_PASSWORD_DB")
}

func TestPostgres_Credentials(t *testing.T) {
	ms, _ := Get("postgres")
	creds := ms.Credentials("db")

	assertKey(t, creds, "HOST", "db")
	assertKey(t, creds, "PORT", "5432")
	assertKey(t, creds, "USER", "postgres")
	assertKey(t, creds, "DATABASE", "db")
	if creds["PASSWORD"] == "" {
		t.Error("PASSWORD should not be empty")
	}
	if !strings.HasPrefix(creds["URL"], "postgres://postgres:") {
		t.Errorf("URL = %q", creds["URL"])
	}
}

func TestPostgres_CredentialsUnique(t *testing.T) {
	ms, _ := Get("postgres")
	c1 := ms.Credentials("db")
	c2 := ms.Credentials("db")
	if c1["PASSWORD"] == c2["PASSWORD"] {
		t.Error("passwords should be unique across calls")
	}
}

func TestPostgres_InternalSecretsNamespaced(t *testing.T) {
	ms, _ := Get("postgres")
	creds := ms.Credentials("db")

	s1 := ms.InternalSecrets("db", creds)
	s2 := ms.InternalSecrets("analytics", creds)

	// Keys should be namespaced — no collision.
	if _, ok := s1["POSTGRES_PASSWORD_DB"]; !ok {
		t.Errorf("db: got keys %v, want POSTGRES_PASSWORD_DB", s1)
	}
	if _, ok := s2["POSTGRES_PASSWORD_ANALYTICS"]; !ok {
		t.Errorf("analytics: got keys %v, want POSTGRES_PASSWORD_ANALYTICS", s2)
	}
}

func TestPostgres_SpecSecretsNamespaced(t *testing.T) {
	ms, _ := Get("postgres")
	spec1 := ms.Spec("db")
	spec2 := ms.Spec("analytics")

	// Each spec should reference its own namespaced secret key.
	assertContains(t, spec1.Secrets, "POSTGRES_PASSWORD=POSTGRES_PASSWORD_DB")
	assertContains(t, spec2.Secrets, "POSTGRES_PASSWORD=POSTGRES_PASSWORD_ANALYTICS")
}

func TestPostgres_EnvPrefix(t *testing.T) {
	ms, _ := Get("postgres")
	if ms.EnvPrefix() != "DATABASE" {
		t.Errorf("prefix = %q", ms.EnvPrefix())
	}
}

// ── Redis ──────────────────────────────────────────────────────────────────────

func TestRedis_Spec(t *testing.T) {
	ms, _ := Get("redis")
	spec := ms.Spec("cache")

	if spec.Image != "redis:7-alpine" {
		t.Errorf("image = %q", spec.Image)
	}
	if spec.Port != 6379 {
		t.Errorf("port = %d", spec.Port)
	}
	if len(spec.Volumes) != 0 {
		t.Errorf("volumes = %v (redis should have no volumes)", spec.Volumes)
	}
}

func TestRedis_Credentials(t *testing.T) {
	ms, _ := Get("redis")
	creds := ms.Credentials("cache")

	assertKey(t, creds, "HOST", "cache")
	assertKey(t, creds, "PORT", "6379")
	assertKey(t, creds, "URL", "redis://cache:6379")
}

func TestRedis_EnvPrefix(t *testing.T) {
	ms, _ := Get("redis")
	if ms.EnvPrefix() != "REDIS" {
		t.Errorf("prefix = %q", ms.EnvPrefix())
	}
}

// ── Meilisearch ────────────────────────────────────────────────────────────────

func TestMeilisearch_Spec(t *testing.T) {
	ms, _ := Get("meilisearch")
	spec := ms.Spec("search")

	if spec.Image != "getmeili/meilisearch:latest" {
		t.Errorf("image = %q", spec.Image)
	}
	if spec.Port != 7700 {
		t.Errorf("port = %d", spec.Port)
	}
	if len(spec.Volumes) != 1 || spec.Volumes[0] != "search-data:/meili_data" {
		t.Errorf("volumes = %v", spec.Volumes)
	}
	assertContains(t, spec.Secrets, "MEILI_MASTER_KEY=MEILI_MASTER_KEY_SEARCH")
}

func TestMeilisearch_Credentials(t *testing.T) {
	ms, _ := Get("meilisearch")
	creds := ms.Credentials("search")

	assertKey(t, creds, "HOST", "search")
	assertKey(t, creds, "PORT", "7700")
	if creds["MASTER_KEY"] == "" {
		t.Error("MASTER_KEY should not be empty")
	}
	if !strings.HasPrefix(creds["URL"], "http://search:7700") {
		t.Errorf("URL = %q", creds["URL"])
	}
}

func TestMeilisearch_EnvPrefix(t *testing.T) {
	ms, _ := Get("meilisearch")
	if ms.EnvPrefix() != "MEILI" {
		t.Errorf("prefix = %q", ms.EnvPrefix())
	}
}

// ── Expand ─────────────────────────────────────────────────────────────────────

func TestExpand_ReplacesManaged(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.Server{
			"master": {Type: "cx23", Region: "fsn1"},
		},
		Services: map[string]config.Service{
			"db":  {Managed: "postgres"},
			"web": {Image: "nginx", Port: 80},
		},
	}

	expanded, newCreds, err := Expand(cfg, nil)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	// db should be replaced with postgres spec.
	db := expanded.Services["db"]
	if db.Managed != "" {
		t.Error("managed field should be cleared after expansion")
	}
	if db.Image != "postgres:17" {
		t.Errorf("db image = %q", db.Image)
	}
	if db.Port != 5432 {
		t.Errorf("db port = %d", db.Port)
	}

	// web should be untouched.
	if expanded.Services["web"].Image != "nginx" {
		t.Error("web should be untouched")
	}

	// Credentials should be generated.
	if _, ok := newCreds["db"]; !ok {
		t.Error("db credentials should be generated")
	}
	if newCreds["db"]["PASSWORD"] == "" {
		t.Error("db password should not be empty")
	}
}

func TestExpand_UsesStoredCreds(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.Server{
			"master": {Type: "cx23", Region: "fsn1"},
		},
		Services: map[string]config.Service{
			"db": {Managed: "postgres"},
		},
	}

	stored := map[string]map[string]string{
		"db": {"HOST": "db", "PORT": "5432", "USER": "postgres", "PASSWORD": "stored-pwd", "DATABASE": "db", "URL": "postgres://postgres:stored-pwd@db:5432/db"},
	}

	_, newCreds, err := Expand(cfg, stored)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	// No new credentials should be generated.
	if len(newCreds) != 0 {
		t.Errorf("should not generate new creds when stored, got %v", newCreds)
	}
}

func TestExpand_InjectsSecretsViaUses(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.Server{
			"master": {Type: "cx23", Region: "fsn1"},
		},
		Services: map[string]config.Service{
			"db":  {Managed: "postgres"},
			"web": {Image: "nginx", Port: 80, Uses: []string{"db"}},
		},
	}

	expanded, _, err := Expand(cfg, nil)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	web := expanded.Services["web"]
	// Should have DATABASE_DB_* secret refs injected.
	secrets := web.Secrets
	sort.Strings(secrets)

	expected := []string{
		"DATABASE_DB_DATABASE",
		"DATABASE_DB_HOST",
		"DATABASE_DB_PASSWORD",
		"DATABASE_DB_PORT",
		"DATABASE_DB_URL",
		"DATABASE_DB_USER",
	}
	if len(secrets) != len(expected) {
		t.Fatalf("secrets = %v, want %v", secrets, expected)
	}
	for i, want := range expected {
		if secrets[i] != want {
			t.Errorf("secrets[%d] = %q, want %q", i, secrets[i], want)
		}
	}

	// Uses should be consumed (nil).
	if web.Uses != nil {
		t.Errorf("uses should be nil after expansion, got %v", web.Uses)
	}
}

func TestExpand_MultipleUses(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.Server{
			"master": {Type: "cx23", Region: "fsn1"},
		},
		Services: map[string]config.Service{
			"db":     {Managed: "postgres"},
			"cache":  {Managed: "redis"},
			"web":    {Image: "nginx", Port: 80, Uses: []string{"db", "cache"}},
		},
	}

	expanded, _, err := Expand(cfg, nil)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	web := expanded.Services["web"]
	// Should have both DATABASE_DB_* and REDIS_CACHE_* secrets.
	hasDB := false
	hasRedis := false
	for _, s := range web.Secrets {
		if strings.HasPrefix(s, "DATABASE_DB_") {
			hasDB = true
		}
		if strings.HasPrefix(s, "REDIS_CACHE_") {
			hasRedis = true
		}
	}
	if !hasDB {
		t.Error("missing DATABASE_DB_* secrets")
	}
	if !hasRedis {
		t.Error("missing REDIS_CACHE_* secrets")
	}
}

func TestExpand_UnknownManagedKind(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.Server{
			"master": {Type: "cx23", Region: "fsn1"},
		},
		Services: map[string]config.Service{
			"db": {Managed: "mongodb"},
		},
	}

	_, _, err := Expand(cfg, nil)
	if err == nil {
		t.Fatal("expected error for unknown managed kind")
	}
	if !strings.Contains(err.Error(), "mongodb") {
		t.Errorf("error = %v, should mention mongodb", err)
	}
}

func TestExpand_DoesNotMutateOriginal(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.Server{
			"master": {Type: "cx23", Region: "fsn1"},
		},
		Services: map[string]config.Service{
			"db":  {Managed: "postgres"},
			"web": {Image: "nginx", Port: 80, Uses: []string{"db"}},
		},
	}

	_, _, err := Expand(cfg, nil)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	// Original should still have managed and uses.
	if cfg.Services["db"].Managed != "postgres" {
		t.Error("original db.managed was mutated")
	}
	if len(cfg.Services["web"].Uses) != 1 {
		t.Error("original web.uses was mutated")
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func assertKey(t *testing.T, m map[string]string, key, want string) {
	t.Helper()
	got, ok := m[key]
	if !ok {
		t.Errorf("key %q not found", key)
		return
	}
	if got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

func assertContains(t *testing.T, list []string, want string) {
	t.Helper()
	for _, s := range list {
		if s == want {
			return
		}
	}
	t.Errorf("list %v does not contain %q", list, want)
}
