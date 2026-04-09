package api

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NOTE: Provider enums (ComputeProvider, DNSProvider, etc.) are gone.
// Provider selection lives on InfraProvider.Name — no enum validation needed.
// Repos link to InfraProviders via FK columns.

func newUUID() string {
	return uuid.NewString()
}

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

// ── InfraProvider ───────────────────────────────────────────────────────────

// ProviderKind is the category of infrastructure provider.
type ProviderKind string

const (
	ProviderKindCompute ProviderKind = "compute"
	ProviderKindDNS     ProviderKind = "dns"
	ProviderKindStorage ProviderKind = "storage"
	ProviderKindBuild   ProviderKind = "build"
)

// InfraProvider stores a provider with its credentials at workspace scope.
// A workspace can have multiple providers (e.g. hetzner + aws compute).
// Repos link to specific providers via FK columns.
type InfraProvider struct {
	ID          string       `gorm:"primaryKey" json:"id"`
	WorkspaceID string       `gorm:"not null;uniqueIndex:idx_infra_ws_kind_name" json:"workspace_id"`
	Kind        ProviderKind `gorm:"not null;uniqueIndex:idx_infra_ws_kind_name" json:"kind"` // compute, dns, storage, build
	Name        string       `gorm:"not null;uniqueIndex:idx_infra_ws_kind_name" json:"name"` // hetzner, cloudflare, aws, daytona, etc.
	Credentials string       `gorm:"type:text;not null" json:"-"`                             // encrypted JSON (schema-mapped)
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`

	Workspace Workspace `gorm:"foreignKey:WorkspaceID" json:"-"`
}

func (InfraProvider) TableName() string { return "infra_providers" }

func (p *InfraProvider) BeforeCreate(tx *gorm.DB) error {
	if p.ID == "" {
		p.ID = newUUID()
	}
	return p.encryptCredentials()
}

func (p *InfraProvider) BeforeUpdate(tx *gorm.DB) error {
	return p.encryptCredentials()
}

func (p *InfraProvider) AfterFind(tx *gorm.DB) error {
	return p.decryptCredentials()
}

func (p *InfraProvider) encryptCredentials() error {
	if p.Credentials == "" {
		return nil
	}
	enc, err := Encrypt(p.Credentials)
	if err != nil {
		return err
	}
	p.Credentials = enc
	return nil
}

func (p *InfraProvider) decryptCredentials() error {
	if p.Credentials == "" {
		return nil
	}
	dec, err := Decrypt(p.Credentials)
	if err != nil {
		return fmt.Errorf("decrypt InfraProvider.Credentials: %w", err)
	}
	p.Credentials = dec
	return nil
}

// CredentialsMap returns the credentials as a map[string]string.
func (p *InfraProvider) CredentialsMap() map[string]string {
	if p == nil || p.Credentials == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(p.Credentials), &m); err != nil {
		return nil
	}
	return m
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

	// Provider links — each repo picks one provider per kind from the workspace.
	ComputeProviderID *string `gorm:"index" json:"compute_provider_id,omitempty"`
	DNSProviderID     *string `gorm:"index" json:"dns_provider_id,omitempty"`
	StorageProviderID *string `gorm:"index" json:"storage_provider_id,omitempty"`
	BuildProviderID   *string `gorm:"index" json:"build_provider_id,omitempty"`

	Workspace       Workspace      `gorm:"foreignKey:WorkspaceID" json:"-"`
	ComputeProvider *InfraProvider `gorm:"foreignKey:ComputeProviderID" json:"compute_provider,omitempty"`
	DNSProvider     *InfraProvider `gorm:"foreignKey:DNSProviderID" json:"dns_provider,omitempty"`
	StorageProvider *InfraProvider `gorm:"foreignKey:StorageProviderID" json:"storage_provider,omitempty"`
	BuildProvider   *InfraProvider `gorm:"foreignKey:BuildProviderID" json:"build_provider,omitempty"`
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

// ── CommandLog ───────────────────────────────────────────────────────────────

// CommandLog records one executed command and its outcome.
// One row per API /run call. No JSONL blob — the CLI renders in real-time.
type CommandLog struct {
	ID         string    `gorm:"primaryKey" json:"id"`
	RepoID     string    `gorm:"not null;index" json:"repo_id"`
	UserID     string    `gorm:"not null" json:"user_id"`
	Kind       string    `gorm:"not null" json:"kind"`   // "instance.set", "service.delete", etc.
	Name       string    `gorm:"not null" json:"name"`   // "master", "web", etc.
	Status     string    `gorm:"not null" json:"status"` // "succeeded" | "failed"
	Error      string    `gorm:"type:text" json:"error,omitempty"`
	DurationMs int       `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`

	Repo Repo `gorm:"foreignKey:RepoID" json:"-"`
	User User `gorm:"foreignKey:UserID" json:"-"`
}

func (CommandLog) TableName() string { return "command_logs" }

func (cl *CommandLog) BeforeCreate(tx *gorm.DB) error {
	if cl.ID == "" {
		cl.ID = newUUID()
	}
	return nil
}
