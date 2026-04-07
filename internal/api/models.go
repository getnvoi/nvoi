package api

import (
	"fmt"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
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
	BuildLocal   BuildProvider = "local"
	BuildDaytona BuildProvider = "daytona"
	BuildGitHub  BuildProvider = "github"
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
	GithubToken    string         `gorm:"type:text;not null" json:"-"` // encrypted, never in JSON
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == "" {
		u.ID = newUUID()
	}
	return u.encryptToken()
}

func (u *User) BeforeUpdate(tx *gorm.DB) error {
	return u.encryptToken()
}

func (u *User) AfterFind(tx *gorm.DB) error {
	return u.decryptToken()
}

func (u *User) encryptToken() error {
	if u.GithubToken == "" {
		return nil
	}
	enc, err := Encrypt(u.GithubToken)
	if err != nil {
		return err
	}
	u.GithubToken = enc
	return nil
}

func (u *User) decryptToken() error {
	if u.GithubToken == "" {
		return nil
	}
	dec, err := Decrypt(u.GithubToken)
	if err != nil {
		return fmt.Errorf("decrypt User.GithubToken: %w", err)
	}
	u.GithubToken = dec
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
	ID            string         `gorm:"primaryKey" json:"id"`
	WorkspaceID   string         `gorm:"not null;index" json:"workspace_id"`
	Name          string         `gorm:"not null" json:"name"`
	Environment   string         `gorm:"not null;default:'production'" json:"environment"` // production, staging, etc.
	SSHPrivateKey string         `gorm:"type:text;not null" json:"-"`                      // encrypted PEM — auto-generated, never nil
	SSHPublicKey  string         `gorm:"not null" json:"ssh_public_key"`                   // OpenSSH format — visible for deploy key setup
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`

	Workspace Workspace `gorm:"foreignKey:WorkspaceID" json:"-"`
}

func (r *Repo) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = newUUID()
	}
	if r.Environment == "" {
		r.Environment = "production"
	}
	// Auto-generate SSH keypair — always, never nil.
	if r.SSHPrivateKey == "" {
		priv, pub, err := utils.GenerateEd25519Key()
		if err != nil {
			return err
		}
		r.SSHPrivateKey = string(priv)
		r.SSHPublicKey = pub
	}
	return r.encryptSSHKey()
}

func (r *Repo) AfterFind(tx *gorm.DB) error {
	return r.decryptSSHKey()
}

func (r *Repo) encryptSSHKey() error {
	if r.SSHPrivateKey == "" {
		return nil
	}
	enc, err := Encrypt(r.SSHPrivateKey)
	if err != nil {
		return err
	}
	r.SSHPrivateKey = enc
	return nil
}

func (r *Repo) decryptSSHKey() error {
	if r.SSHPrivateKey == "" {
		return nil
	}
	dec, err := Decrypt(r.SSHPrivateKey)
	if err != nil {
		return fmt.Errorf("decrypt Repo.SSHPrivateKey: %w", err)
	}
	r.SSHPrivateKey = dec
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

// ── RepoManagedServiceConfig (permanent credentials) ─────────────────────────

// RepoManagedServiceConfig stores generated credentials for a managed service.
// Created once when a managed service first appears in a config push.
// Not versioned. Row exists = inject at deploy time. Row deleted = stop injecting.
type RepoManagedServiceConfig struct {
	ID          string    `gorm:"primaryKey" json:"id"`
	RepoID      string    `gorm:"not null;uniqueIndex:idx_repo_managed_svc" json:"repo_id"`
	Name        string    `gorm:"not null;uniqueIndex:idx_repo_managed_svc" json:"name"` // "db", "cache", "search"
	Kind        string    `gorm:"not null" json:"kind"`                                  // "postgres", "redis", "meilisearch"
	Credentials string    `gorm:"type:text;not null" json:"-"`                           // encrypted JSON
	CreatedAt   time.Time `json:"created_at"`

	Repo Repo `gorm:"foreignKey:RepoID" json:"-"`
}

func (RepoManagedServiceConfig) TableName() string { return "repo_managed_service_configs" }

func (r *RepoManagedServiceConfig) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = newUUID()
	}
	return r.encryptCredentials()
}

func (r *RepoManagedServiceConfig) AfterFind(tx *gorm.DB) error {
	return r.decryptCredentials()
}

func (r *RepoManagedServiceConfig) encryptCredentials() error {
	if r.Credentials == "" {
		return nil
	}
	enc, err := Encrypt(r.Credentials)
	if err != nil {
		return err
	}
	r.Credentials = enc
	return nil
}

func (r *RepoManagedServiceConfig) decryptCredentials() error {
	if r.Credentials == "" {
		return nil
	}
	dec, err := Decrypt(r.Credentials)
	if err != nil {
		return fmt.Errorf("decrypt RepoManagedServiceConfig.Credentials: %w", err)
	}
	r.Credentials = dec
	return nil
}

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

// ── Deployment ───────────────────────────────────────────────────────────────

type DeploymentStatus string

const (
	DeploymentPending   DeploymentStatus = "pending"
	DeploymentRunning   DeploymentStatus = "running"
	DeploymentSucceeded DeploymentStatus = "succeeded"
	DeploymentFailed    DeploymentStatus = "failed"
)

// Deployment tracks a deploy run for a repo config version.
type Deployment struct {
	ID           string           `gorm:"primaryKey" json:"id"`
	RepoID       string           `gorm:"not null;index" json:"repo_id"`
	RepoConfigID string           `gorm:"not null" json:"repo_config_id"`
	Status       DeploymentStatus `gorm:"not null;default:'pending'" json:"status"`
	CreatedAt    time.Time        `json:"created_at"`
	StartedAt    *time.Time       `json:"started_at,omitempty"`
	FinishedAt   *time.Time       `json:"finished_at,omitempty"`

	Repo       Repo             `gorm:"foreignKey:RepoID" json:"-"`
	RepoConfig RepoConfig       `gorm:"foreignKey:RepoConfigID" json:"-"`
	Steps      []DeploymentStep `gorm:"foreignKey:DeploymentID" json:"steps,omitempty"`
}

func (Deployment) TableName() string { return "deployments" }

func (d *Deployment) BeforeCreate(tx *gorm.DB) error {
	if d.ID == "" {
		d.ID = newUUID()
	}
	return nil
}

// ── DeploymentStep ───────────────────────────────────────────────────────────

type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusSucceeded StepStatus = "succeeded"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
)

// DeploymentStep is one action in a deployment plan.
type DeploymentStep struct {
	ID           string     `gorm:"primaryKey" json:"id"`
	DeploymentID string     `gorm:"not null;index" json:"deployment_id"`
	Position     int        `gorm:"not null" json:"position"`
	Kind         string     `gorm:"not null" json:"kind"`    // "instance.set", "service.delete", etc.
	Name         string     `gorm:"not null" json:"name"`    // "master", "web", etc.
	Params       string     `gorm:"type:text" json:"params"` // JSON
	Status       StepStatus `gorm:"not null;default:'pending'" json:"status"`
	Error        string     `gorm:"type:text" json:"error,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`

	Deployment Deployment          `gorm:"foreignKey:DeploymentID" json:"-"`
	Logs       []DeploymentStepLog `gorm:"foreignKey:DeploymentStepID" json:"logs,omitempty"`
}

func (DeploymentStep) TableName() string { return "deployment_steps" }

func (s *DeploymentStep) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = newUUID()
	}
	return nil
}

// ── DeploymentStepLog ────────────────────────────────────────────────────────

// DeploymentStepLog stores one JSONL line emitted during step execution.
// Same event format as --json output: {"type":"progress","message":"..."}
type DeploymentStepLog struct {
	ID               string    `gorm:"primaryKey" json:"id"`
	DeploymentStepID string    `gorm:"not null;index" json:"deployment_step_id"`
	Line             string    `gorm:"type:text;not null" json:"line"` // JSONL
	CreatedAt        time.Time `json:"created_at"`

	DeploymentStep DeploymentStep `gorm:"foreignKey:DeploymentStepID" json:"-"`
}

func (DeploymentStepLog) TableName() string { return "deployment_step_logs" }

func (l *DeploymentStepLog) BeforeCreate(tx *gorm.DB) error {
	if l.ID == "" {
		l.ID = newUUID()
	}
	return nil
}
