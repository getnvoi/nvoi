package neon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type Provider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func New(creds map[string]string) *Provider {
	base := creds["base_url"]
	if base == "" {
		base = "https://console.neon.tech/api/v2"
	}
	return &Provider{
		apiKey:  creds["api_key"],
		baseURL: strings.TrimRight(base, "/"),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *Provider) Close() error { return nil }

func (p *Provider) ListResources(context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}

func (p *Provider) ValidateCredentials(ctx context.Context) error {
	_, err := p.listProjects(ctx)
	return err
}

func (p *Provider) EnsureCredentials(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest) (provider.DatabaseCredentials, error) {
	if kc != nil {
		if creds, err := readSecretCredentials(ctx, kc, req); err == nil && creds.URL != "" {
			return creds, nil
		}
	}

	project, branch, err := p.ensureProject(ctx, req)
	if err != nil {
		return provider.DatabaseCredentials{}, err
	}
	password, err := p.revealPassword(ctx, project.ID, branch.ID, branch.RoleName)
	if err != nil {
		return provider.DatabaseCredentials{}, err
	}
	creds := provider.DatabaseCredentials{
		URL:      fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=require", branch.RoleName, password, branch.Host, branch.DatabaseName),
		Host:     branch.Host,
		Port:     5432,
		User:     branch.RoleName,
		Password: password,
		Database: branch.DatabaseName,
		SSLMode:  "require",
	}
	if kc != nil {
		if err := writeSecretCredentials(ctx, kc, req, creds); err != nil {
			return provider.DatabaseCredentials{}, err
		}
	}
	return creds, nil
}

// Reconcile emits the shared backup CronJob when backups are configured.
// Neon has no in-cluster workloads — the database itself lives at the
// Neon API — so the only thing Reconcile produces is the uniform dump
// pipeline, pointed at the external endpoint via DATABASE_URL from the
// credentials Secret written by EnsureCredentials.
func (p *Provider) Reconcile(_ context.Context, req provider.DatabaseRequest) (*provider.DatabasePlan, error) {
	var workloads []runtime.Object
	if req.Spec.Backup != nil && req.Spec.Backup.Schedule != "" {
		workloads = append(workloads, provider.BuildBackupCronJob(req))
	}
	return &provider.DatabasePlan{Workloads: workloads}, nil
}

func (p *Provider) Delete(ctx context.Context, req provider.DatabaseRequest) error {
	project, err := p.findProjectByName(ctx, req.FullName)
	if err != nil {
		return err
	}
	if project == nil {
		return nil
	}
	_, err = p.request(ctx, http.MethodDelete, "/projects/"+project.ID, nil)
	return err
}

func (p *Provider) ExecSQL(ctx context.Context, req provider.DatabaseRequest, stmt string) (*provider.SQLResult, error) {
	project, err := p.findProjectByName(ctx, req.FullName)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("neon project %q not found", req.FullName)
	}
	resp, err := p.sqlRequest(ctx, project.ID, stmt)
	if err != nil {
		return nil, err
	}
	var out provider.SQLResult
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BackupNow kicks a one-shot Job from the scheduled CronJob template.
// The CronJob body — neon image, pg_dump against DATABASE_URL, gzip,
// sigv4 upload — is identical to postgres/mysql selfhosted and
// planetscale. Uniform pull-then-put semantics mean `BackupNow` is a
// thin wrapper over `CreateJobFromCronJob` in every provider.
//
// Neon's native branch primitive is no longer the backup of record:
// branches give zero-cost PITR but fragment the list/download surface.
// nvoi promises "gzipped dumps in a bucket" uniformly; operators who
// want branches still have the Neon UI.
func (p *Provider) BackupNow(ctx context.Context, req provider.DatabaseRequest) (*provider.BackupRef, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("neon BackupNow requires kube client")
	}
	if req.Spec.Backup == nil || req.Spec.Backup.Schedule == "" {
		return nil, fmt.Errorf("neon backup schedule is not configured")
	}
	jobName := fmt.Sprintf("%s-manual-%d", req.BackupName, time.Now().Unix())
	if err := req.Kube.CreateJobFromCronJob(ctx, req.Namespace, req.BackupName, jobName); err != nil {
		return nil, err
	}
	return &provider.BackupRef{ID: jobName, CreatedAt: time.Now().UTC().Format(time.RFC3339), Kind: "dump"}, nil
}

// ListBackups / DownloadBackup delegate to the shared bucket helpers —
// every engine's backup artifact is a gzipped logical dump in the
// same object-store layout, so there's nothing engine-specific here.
func (p *Provider) ListBackups(ctx context.Context, req provider.DatabaseRequest) ([]provider.BackupRef, error) {
	return provider.BucketListBackups(ctx, req)
}

func (p *Provider) DownloadBackup(ctx context.Context, req provider.DatabaseRequest, backupID string, w io.Writer) error {
	return provider.BucketDownloadBackup(ctx, req, backupID, w)
}

// Restore delegates to the uniform Job-based substrate. Neon's
// DATABASE_URL points at `{branch.host}:5432` (external TLS); the
// restore Job runs `psql` against it exactly the same way the backup
// Job runs `pg_dump` against it. No Neon-specific API surface — we
// treat Neon as a pg-wire endpoint and replay our own artifact.
func (p *Provider) Restore(ctx context.Context, req provider.DatabaseRequest, backupKey string) error {
	return provider.RunRestoreJob(ctx, req.Kube, req, backupKey)
}

type neonProject struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DefaultBranchID string `json:"default_branch_id"`
}

type neonResolvedBranch struct {
	ID           string
	CreatedAt    string
	Host         string
	RoleName     string
	DatabaseName string
}

func (p *Provider) listProjects(ctx context.Context) ([]neonProject, error) {
	resp, err := p.request(ctx, http.MethodGet, "/projects", nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Projects []neonProject `json:"projects"`
	}
	if err := json.Unmarshal(resp, &payload); err != nil {
		return nil, err
	}
	return payload.Projects, nil
}

func (p *Provider) findProjectByName(ctx context.Context, name string) (*neonProject, error) {
	projects, err := p.listProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		if project.Name == name {
			cp := project
			return &cp, nil
		}
	}
	return nil, nil
}

func (p *Provider) ensureProject(ctx context.Context, req provider.DatabaseRequest) (*neonProject, *neonResolvedBranch, error) {
	if project, err := p.findProjectByName(ctx, req.FullName); err != nil {
		return nil, nil, err
	} else if project != nil {
		branch, err := p.getProjectBranch(ctx, project.ID, project.DefaultBranchID)
		return project, branch, err
	}
	body := map[string]any{
		"project": map[string]any{
			"name":      req.FullName,
			"region_id": req.Spec.Region,
		},
	}
	resp, err := p.request(ctx, http.MethodPost, "/projects", body)
	if err != nil {
		return nil, nil, err
	}
	var payload struct {
		Project neonProject `json:"project"`
		Branch  struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			CreatedAt    string `json:"created_at"`
			RoleName     string `json:"role_name"`
			DatabaseName string `json:"database_name"`
			Host         string `json:"host"`
		} `json:"branch"`
	}
	if err := json.Unmarshal(resp, &payload); err != nil {
		return nil, nil, err
	}
	payload.Project.DefaultBranchID = payload.Branch.ID
	return &payload.Project, &neonResolvedBranch{
		ID:           payload.Branch.ID,
		CreatedAt:    payload.Branch.CreatedAt,
		Host:         payload.Branch.Host,
		RoleName:     payload.Branch.RoleName,
		DatabaseName: payload.Branch.DatabaseName,
	}, nil
}

func (p *Provider) getProjectBranch(ctx context.Context, projectID, branchID string) (*neonResolvedBranch, error) {
	resp, err := p.request(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/branches/%s", projectID, branchID), nil)
	if err != nil {
		return nil, err
	}
	return parseNeonBranch(resp)
}

func parseNeonBranch(resp []byte) (*neonResolvedBranch, error) {
	var payload struct {
		Branch struct {
			ID           string `json:"id"`
			CreatedAt    string `json:"created_at"`
			RoleName     string `json:"role_name"`
			DatabaseName string `json:"database_name"`
			Host         string `json:"host"`
		} `json:"branch"`
	}
	if err := json.Unmarshal(resp, &payload); err != nil {
		return nil, err
	}
	return &neonResolvedBranch{
		ID:           payload.Branch.ID,
		CreatedAt:    payload.Branch.CreatedAt,
		Host:         payload.Branch.Host,
		RoleName:     payload.Branch.RoleName,
		DatabaseName: payload.Branch.DatabaseName,
	}, nil
}

func (p *Provider) revealPassword(ctx context.Context, projectID, branchID, roleName string) (string, error) {
	resp, err := p.request(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/branches/%s/roles/%s/reveal_password", projectID, branchID, roleName), nil)
	if err != nil {
		return "", err
	}
	var payload struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(resp, &payload); err != nil {
		return "", err
	}
	if payload.Password == "" {
		return "", fmt.Errorf("neon reveal_password returned empty password")
	}
	return payload.Password, nil
}

func (p *Provider) sqlRequest(ctx context.Context, projectID, stmt string) ([]byte, error) {
	sqlURL := p.baseURL + "/sql"
	if strings.EqualFold(p.baseURL, "https://console.neon.tech/api/v2") {
		sqlURL = p.baseURL + "/sql"
	}
	body := map[string]any{"project_id": projectID, "query": stmt}
	return p.requestAbsolute(ctx, http.MethodPost, sqlURL, body)
}

func readSecretCredentials(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest) (provider.DatabaseCredentials, error) {
	urlValue, err := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "url")
	if err != nil {
		return provider.DatabaseCredentials{}, err
	}
	host, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "host")
	user, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "user")
	password, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "password")
	database, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "database")
	return provider.DatabaseCredentials{
		URL:      urlValue,
		Host:     host,
		Port:     5432,
		User:     user,
		Password: password,
		Database: database,
		SSLMode:  "require",
	}, nil
}

func writeSecretCredentials(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest, creds provider.DatabaseCredentials) error {
	return kc.EnsureSecret(ctx, req.Namespace, utils.OwnerDatabases, req.CredentialsSecretName, map[string]string{
		"url":      creds.URL,
		"host":     creds.Host,
		"port":     strconv.Itoa(creds.Port),
		"user":     creds.User,
		"password": creds.Password,
		"database": creds.Database,
		"sslmode":  creds.SSLMode,
	})
}

func (p *Provider) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	return p.requestAbsolute(ctx, method, p.baseURL+path, body)
}

func (p *Provider) requestAbsolute(ctx context.Context, method, target string, body any) ([]byte, error) {
	var r io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return nil, err
		}
		r = buf
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("neon API %s %s: %s", method, pathForError(target), strings.TrimSpace(string(b)))
	}
	return b, nil
}

func pathForError(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}
