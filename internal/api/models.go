package api

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func newUUID() string {
	return uuid.NewString()
}

// ── Provider enums ───────────────────────────────────────────────────────────

type ComputeProvider string

const (
	ComputeHetzner  ComputeProvider = "hetzner"
	ComputeAWS      ComputeProvider = "aws"
	ComputeScaleway ComputeProvider = "scaleway"
)

var validComputeProviders = map[ComputeProvider]bool{
	ComputeHetzner:  true,
	ComputeAWS:      true,
	ComputeScaleway: true,
}

func (p ComputeProvider) Valid() bool { return validComputeProviders[p] }

type DNSProvider string

const (
	DNSCloudflare DNSProvider = "cloudflare"
	DNSAWS        DNSProvider = "aws"
)

var validDNSProviders = map[DNSProvider]bool{
	DNSCloudflare: true,
	DNSAWS:        true,
}

func (p DNSProvider) Valid() bool { return validDNSProviders[p] }

type StorageProvider string

const (
	StorageCloudflare StorageProvider = "cloudflare"
	StorageAWS        StorageProvider = "aws"
)

var validStorageProviders = map[StorageProvider]bool{
	StorageCloudflare: true,
	StorageAWS:        true,
}

func (p StorageProvider) Valid() bool { return validStorageProviders[p] }

type BuildProvider string

const (
	BuildLocal  BuildProvider = "local"
	BuildDaytona BuildProvider = "daytona"
	BuildGitHub BuildProvider = "github"
)

var validBuildProviders = map[BuildProvider]bool{
	BuildLocal:   true,
	BuildDaytona: true,
	BuildGitHub:  true,
}

func (p BuildProvider) Valid() bool { return validBuildProviders[p] }

type User struct {
	ID             string         `gorm:"primaryKey" json:"id"`
	GithubUsername string         `gorm:"uniqueIndex;not null" json:"github_username"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == "" {
		u.ID = newUUID()
	}
	return nil
}

// ── Workspace ─────────────────────────────────────────────────────────────────

type Workspace struct {
	ID        string         `gorm:"primaryKey" json:"id"`
	Name      string         `gorm:"not null" json:"name"`
	CreatedBy string         `gorm:"not null;index" json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	Creator User   `gorm:"foreignKey:CreatedBy" json:"-"`
	Repos   []Repo `gorm:"foreignKey:WorkspaceID" json:"repos,omitempty"`
}

func (w *Workspace) BeforeCreate(tx *gorm.DB) error {
	if w.ID == "" {
		w.ID = newUUID()
	}
	return nil
}

// ── WorkspaceUser (join table) ────────────────────────────────────────────────

type WorkspaceUser struct {
	ID          string    `gorm:"primaryKey" json:"id"`
	UserID      string    `gorm:"not null;uniqueIndex:idx_ws_user" json:"user_id"`
	WorkspaceID string    `gorm:"not null;uniqueIndex:idx_ws_user" json:"workspace_id"`
	Role        string    `gorm:"not null;default:'owner'" json:"role"`
	CreatedAt   time.Time `json:"created_at"`

	User      User      `gorm:"foreignKey:UserID" json:"-"`
	Workspace Workspace `gorm:"foreignKey:WorkspaceID" json:"-"`
}

func (WorkspaceUser) TableName() string { return "workspace_users" }

func (wu *WorkspaceUser) BeforeCreate(tx *gorm.DB) error {
	if wu.ID == "" {
		wu.ID = newUUID()
	}
	return nil
}

// ── Repo ──────────────────────────────────────────────────────────────────────

type Repo struct {
	ID          string         `gorm:"primaryKey" json:"id"`
	WorkspaceID string         `gorm:"not null;index" json:"workspace_id"`
	Name        string         `gorm:"not null" json:"name"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`

	Workspace Workspace `gorm:"foreignKey:WorkspaceID" json:"-"`
}

func (r *Repo) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = newUUID()
	}
	return nil
}

// ── RepoConfig (versioned) ───────────────────────────────────────────────────

// RepoConfig stores a versioned config snapshot for a repo.
// Every push creates a new version. Config is YAML, env is KEY=VALUE pairs.
// Provider selection is explicit typed columns. Credentials stay in env (encrypted).
type RepoConfig struct {
	ID              string          `gorm:"primaryKey" json:"id"`
	RepoID          string          `gorm:"not null;index" json:"repo_id"`
	Version         int             `gorm:"not null" json:"version"`
	ComputeProvider ComputeProvider `gorm:"not null" json:"compute_provider"`
	DNSProvider     DNSProvider     `json:"dns_provider,omitempty"`
	StorageProvider StorageProvider `json:"storage_provider,omitempty"`
	BuildProvider   BuildProvider   `json:"build_provider,omitempty"`
	Config          string          `gorm:"type:text;not null" json:"config"` // YAML
	Env             string          `gorm:"type:text" json:"-"`               // encrypted KEY=VALUE (hidden from JSON)
	CreatedAt       time.Time       `json:"created_at"`

	Repo Repo `gorm:"foreignKey:RepoID" json:"-"`
}

// ValidateProviders checks that all specified providers are valid enum values.
func (rc *RepoConfig) ValidateProviders() error {
	if !rc.ComputeProvider.Valid() {
		return fmt.Errorf("compute_provider: %q is not valid", rc.ComputeProvider)
	}
	if rc.DNSProvider != "" && !rc.DNSProvider.Valid() {
		return fmt.Errorf("dns_provider: %q is not valid", rc.DNSProvider)
	}
	if rc.StorageProvider != "" && !rc.StorageProvider.Valid() {
		return fmt.Errorf("storage_provider: %q is not valid", rc.StorageProvider)
	}
	if rc.BuildProvider != "" && !rc.BuildProvider.Valid() {
		return fmt.Errorf("build_provider: %q is not valid", rc.BuildProvider)
	}
	return nil
}

func (RepoConfig) TableName() string { return "repo_configs" }

func (rc *RepoConfig) BeforeCreate(tx *gorm.DB) error {
	if rc.ID == "" {
		rc.ID = newUUID()
	}
	return rc.encryptEnv()
}

func (rc *RepoConfig) AfterFind(tx *gorm.DB) error {
	return rc.decryptEnv()
}

func (rc *RepoConfig) encryptEnv() error {
	if rc.Env == "" {
		return nil
	}
	enc, err := Encrypt(rc.Env)
	if err != nil {
		return err
	}
	rc.Env = enc
	return nil
}

func (rc *RepoConfig) decryptEnv() error {
	if rc.Env == "" {
		return nil
	}
	dec, err := Decrypt(rc.Env)
	if err != nil {
		return fmt.Errorf("decrypt RepoConfig.Env: %w", err)
	}
	rc.Env = dec
	return nil
}
