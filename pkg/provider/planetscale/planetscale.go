package planetscale

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Provider implements DatabaseProvider for PlanetScale (Vitess-backed
// MySQL). Credentials are minted via the org/service-token API; data-
// plane SQL goes over the public HTTP Data API so `nvoi database sql`
// has no cluster dependency — uniform with neon, unlike postgres which
// exec's into its in-cluster StatefulSet pod.
type Provider struct {
	token        string
	organization string
	baseURL      string
	client       *http.Client
}

func New(creds map[string]string) *Provider {
	base := creds["base_url"]
	if base == "" {
		base = "https://api.planetscale.com/v1"
	}
	return &Provider{
		token:        creds["service_token"],
		organization: creds["organization"],
		baseURL:      strings.TrimRight(base, "/"),
		client:       &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *Provider) ValidateCredentials(ctx context.Context) error {
	_, err := p.request(ctx, http.MethodGet, fmt.Sprintf("/organizations/%s/databases", p.organization), nil)
	return err
}

// EnsureCredentials reads the cluster-side Secret when it exists,
// otherwise provisions the database + issues a scoped role password
// (PlanetScale surfaces `plain_text` exactly once at creation) and
// writes the resulting DSN into the Secret. The Secret IS the
// reconciliation state — drift here is recovered via rotate (delete +
// recreate) in a future pass; v1 trusts a present Secret.
func (p *Provider) EnsureCredentials(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest) (provider.DatabaseCredentials, error) {
	if kc != nil {
		if creds, err := readPSSecret(ctx, kc, req); err == nil && creds.URL != "" {
			return creds, nil
		}
	}
	if err := p.ensureDatabase(ctx, req.FullName); err != nil {
		return provider.DatabaseCredentials{}, err
	}
	password, err := p.ensurePassword(ctx, req.FullName)
	if err != nil {
		return provider.DatabaseCredentials{}, err
	}
	host := fmt.Sprintf("%s.%s.psdb.cloud", req.FullName, p.organization)
	creds := provider.DatabaseCredentials{
		URL:      fmt.Sprintf("mysql://%s:%s@%s/%s?ssl-mode=REQUIRED", password.Username, password.PlainText, host, req.FullName),
		Host:     host,
		Port:     3306,
		User:     password.Username,
		Password: password.PlainText,
		Database: req.FullName,
		SSLMode:  "require",
	}
	if kc != nil {
		if err := writePSSecret(ctx, kc, req, creds); err != nil {
			return provider.DatabaseCredentials{}, err
		}
	}
	return creds, nil
}

// Reconcile emits the shared backup CronJob when backups are configured.
// Same shape as neon: no in-cluster workloads for the database itself;
// only the uniform dump pipeline, pointed at the external endpoint
// (`{fullname}.{org}.psdb.cloud:3306`) via DATABASE_URL from the
// credentials Secret written by EnsureCredentials.
func (p *Provider) Reconcile(_ context.Context, req provider.DatabaseRequest) (*provider.DatabasePlan, error) {
	var workloads []runtime.Object
	if req.Spec.Backup != nil && req.Spec.Backup.Schedule != "" {
		workloads = append(workloads, provider.BuildBackupCronJob(req))
	}
	return &provider.DatabasePlan{Workloads: workloads}, nil
}

func (p *Provider) Delete(ctx context.Context, req provider.DatabaseRequest) error {
	_, err := p.request(ctx, http.MethodDelete, fmt.Sprintf("/organizations/%s/databases/%s", p.organization, req.FullName), nil)
	return err
}

// ExecSQL routes through PlanetScale's HTTP Data API — the gRPC-over-
// HTTP endpoint at `{host}/psdb.v1alpha1.Database/Execute` accepts
// HTTP Basic auth using the issued role password. Symmetric with
// neon.ExecSQL (which POSTs to `/sql`): no cluster, no mysql driver,
// no pod spawn. The wire format is unusual — rows are a single
// base64-encoded byte buffer sliced by per-field `lengths` — so the
// decoder lives below; column/row counts round-trip as typed values.
//
// The DSN comes from EnsureCredentials (cluster Secret if present,
// otherwise minted fresh). This preserves the "credentials live in the
// Secret" contract without forcing the CLI to read it directly.
func (p *Provider) ExecSQL(ctx context.Context, req provider.DatabaseRequest, stmt string) (*provider.SQLResult, error) {
	creds, err := p.EnsureCredentials(ctx, req.Kube, req)
	if err != nil {
		return nil, err
	}
	if creds.User == "" || creds.Password == "" || creds.Host == "" {
		return nil, fmt.Errorf("planetscale ExecSQL: credentials missing for %s", req.Name)
	}
	return p.execHTTP(ctx, creds, stmt)
}

// BackupNow — uniform one-shot Job path. Same body as postgres / neon.
func (p *Provider) BackupNow(ctx context.Context, req provider.DatabaseRequest) (*provider.BackupRef, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("planetscale BackupNow requires kube client")
	}
	if req.Spec.Backup == nil || req.Spec.Backup.Schedule == "" {
		return nil, fmt.Errorf("planetscale backup schedule is not configured")
	}
	jobName := fmt.Sprintf("%s-manual-%d", req.BackupName, time.Now().Unix())
	if err := req.Kube.CreateJobFromCronJob(ctx, req.Namespace, req.BackupName, jobName); err != nil {
		return nil, err
	}
	return &provider.BackupRef{ID: jobName, CreatedAt: time.Now().UTC().Format(time.RFC3339), Kind: "dump"}, nil
}

// ListBackups / DownloadBackup delegate to the shared bucket helpers.
func (p *Provider) ListBackups(ctx context.Context, req provider.DatabaseRequest) ([]provider.BackupRef, error) {
	return provider.BucketListBackups(ctx, req)
}

func (p *Provider) DownloadBackup(ctx context.Context, req provider.DatabaseRequest, backupID string, w io.Writer) error {
	return provider.BucketDownloadBackup(ctx, req, backupID, w)
}

// Restore delegates to the uniform Job-based substrate. PlanetScale's
// DATABASE_URL points at `{db}.{org}.psdb.cloud:3306` (external TLS);
// the restore Job runs `mysql` against it the same way the backup Job
// runs `mysqldump` against it. No PlanetScale-specific API surface —
// we treat PlanetScale as a mysql-wire endpoint and replay our own
// artifact.
func (p *Provider) Restore(ctx context.Context, req provider.DatabaseRequest, backupKey string) error {
	return provider.RunRestoreJob(ctx, req.Kube, req, backupKey)
}

func (p *Provider) ListResources(context.Context) ([]provider.ResourceGroup, error) { return nil, nil }
func (p *Provider) Close() error                                                    { return nil }

type psPassword struct {
	Username  string `json:"username"`
	PlainText string `json:"plain_text"`
}

func (p *Provider) ensureDatabase(ctx context.Context, name string) error {
	_, err := p.request(ctx, http.MethodPost, fmt.Sprintf("/organizations/%s/databases", p.organization), map[string]any{"name": name})
	if err == nil || strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

func (p *Provider) ensurePassword(ctx context.Context, dbName string) (*psPassword, error) {
	resp, err := p.request(ctx, http.MethodPost, fmt.Sprintf("/organizations/%s/databases/%s/passwords", p.organization, dbName), map[string]any{"name": "nvoi"})
	if err != nil {
		return nil, err
	}
	var pw psPassword
	if err := json.Unmarshal(resp, &pw); err != nil {
		return nil, err
	}
	if pw.PlainText == "" {
		return nil, fmt.Errorf("planetscale password response missing plain_text")
	}
	return &pw, nil
}

// execHTTP POSTs to PlanetScale's HTTP Data API. The response's
// `result.rows[*].values` is a single base64-encoded buffer; each
// column's raw bytes are sliced out using the parallel `lengths`
// array (a length of "-1" means SQL NULL).
//
// Data-plane host is `{db}.{org}.connect.psdb.cloud` — distinct from
// the wire-protocol host at `{db}.{org}.psdb.cloud:3306`. The path
// `/psdb.v1alpha1.Database/Execute` is the stable gRPC-over-HTTP
// endpoint the `@planetscale/database` npm client talks to.
//
// The test fake rides this same shape — planetscalefake.go serves
// `/psdb.v1alpha1.Database/Execute` and encodes rows in the same
// lengths+values format.
func (p *Provider) execHTTP(ctx context.Context, creds provider.DatabaseCredentials, stmt string) (*provider.SQLResult, error) {
	host := creds.Host
	// Translate the wire-protocol host to the Data API host. Tests can
	// override by embedding the `connect.` marker themselves.
	if strings.Contains(host, ".psdb.cloud") && !strings.Contains(host, "connect.") {
		host = strings.Replace(host, ".psdb.cloud", ".connect.psdb.cloud", 1)
	}
	target := "https://" + host + "/psdb.v1alpha1.Database/Execute"
	// In test mode the provider's baseURL points at the fake's httptest
	// server. The fake serves the Execute handler directly, so target the
	// fake instead of the real connect.psdb.cloud hostname.
	if strings.HasPrefix(p.baseURL, "http://127.") || strings.HasPrefix(p.baseURL, "http://localhost") {
		target = p.baseURL + "/psdb.v1alpha1.Database/Execute"
	}

	body, err := json.Marshal(map[string]string{"query": stmt})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	auth := base64.StdEncoding.EncodeToString([]byte(creds.User + ":" + creds.Password))
	req.Header.Set("Authorization", "Basic "+auth)

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
		return nil, fmt.Errorf("planetscale execute %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}

	var payload struct {
		Result struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
			RowsAffected string `json:"rowsAffected"`
			Rows         []struct {
				Lengths []string `json:"lengths"`
				Values  string   `json:"values"`
			} `json:"rows"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, fmt.Errorf("planetscale execute decode: %w", err)
	}

	cols := make([]string, 0, len(payload.Result.Fields))
	for _, f := range payload.Result.Fields {
		cols = append(cols, f.Name)
	}

	rows := make([][]string, 0, len(payload.Result.Rows))
	for _, r := range payload.Result.Rows {
		decoded, err := base64.StdEncoding.DecodeString(r.Values)
		if err != nil {
			return nil, fmt.Errorf("planetscale execute row decode: %w", err)
		}
		row := make([]string, 0, len(r.Lengths))
		offset := 0
		for _, lenStr := range r.Lengths {
			n, err := strconv.Atoi(lenStr)
			if err != nil {
				return nil, fmt.Errorf("planetscale execute length parse: %w", err)
			}
			if n < 0 {
				// SQL NULL — render as empty string, matching psql --csv.
				row = append(row, "")
				continue
			}
			if offset+n > len(decoded) {
				return nil, fmt.Errorf("planetscale execute row truncated (offset=%d, len=%d, buffer=%d)", offset, n, len(decoded))
			}
			row = append(row, string(decoded[offset:offset+n]))
			offset += n
		}
		rows = append(rows, row)
	}

	affected, _ := strconv.ParseInt(payload.Result.RowsAffected, 10, 64)
	if affected == 0 {
		affected = int64(len(rows))
	}
	return &provider.SQLResult{Columns: cols, Rows: rows, RowsAffected: affected}, nil
}

func readPSSecret(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest) (provider.DatabaseCredentials, error) {
	urlValue, err := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "url")
	if err != nil {
		return provider.DatabaseCredentials{}, err
	}
	host, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "host")
	user, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "user")
	password, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "password")
	database, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "database")
	return provider.DatabaseCredentials{URL: urlValue, Host: host, Port: 3306, User: user, Password: password, Database: database, SSLMode: "require"}, nil
}

func writePSSecret(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest, creds provider.DatabaseCredentials) error {
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
	var r io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return nil, err
		}
		r = buf
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
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
		return nil, fmt.Errorf("planetscale API %s %s: %s", method, path, strings.TrimSpace(string(b)))
	}
	return b, nil
}
