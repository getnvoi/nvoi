package planetscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

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
		URL:      fmt.Sprintf("mysql://%s:%s@%s/%s", password.Username, password.PlainText, host, req.FullName),
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

func (p *Provider) Reconcile(context.Context, provider.DatabaseRequest) (*provider.DatabasePlan, error) {
	return &provider.DatabasePlan{}, nil
}

func (p *Provider) Delete(ctx context.Context, req provider.DatabaseRequest) error {
	_, err := p.request(ctx, http.MethodDelete, fmt.Sprintf("/organizations/%s/databases/%s", p.organization, req.FullName), nil)
	return err
}

func (p *Provider) ExecSQL(context.Context, provider.DatabaseRequest, string) (*provider.SQLResult, error) {
	return nil, fmt.Errorf("planetscale does not expose a provider SQL execution endpoint")
}

func (p *Provider) BackupNow(ctx context.Context, req provider.DatabaseRequest) (*provider.BackupRef, error) {
	body := map[string]any{"name": fmt.Sprintf("backup-%d", time.Now().Unix())}
	resp, err := p.request(ctx, http.MethodPost, fmt.Sprintf("/organizations/%s/databases/%s/branches", p.organization, req.FullName), body)
	if err != nil {
		return nil, err
	}
	var payload struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(resp, &payload); err != nil {
		return nil, err
	}
	return &provider.BackupRef{ID: payload.ID, CreatedAt: payload.CreatedAt, Kind: "branch"}, nil
}

func (p *Provider) ListBackups(ctx context.Context, req provider.DatabaseRequest) ([]provider.BackupRef, error) {
	resp, err := p.request(ctx, http.MethodGet, fmt.Sprintf("/organizations/%s/databases/%s/branches", p.organization, req.FullName), nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Branches []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			CreatedAt string `json:"created_at"`
		} `json:"branches"`
	}
	if err := json.Unmarshal(resp, &payload); err != nil {
		return nil, err
	}
	out := make([]provider.BackupRef, 0, len(payload.Branches))
	for _, branch := range payload.Branches {
		if !strings.HasPrefix(branch.Name, "backup-") {
			continue
		}
		out = append(out, provider.BackupRef{ID: branch.ID, CreatedAt: branch.CreatedAt, Kind: "branch"})
	}
	return out, nil
}

func (p *Provider) DownloadBackup(context.Context, provider.DatabaseRequest, string, io.Writer) error {
	return fmt.Errorf("planetscale backup download is not supported by this provider")
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
	return kc.EnsureSecret(ctx, req.Namespace, req.CredentialsSecretName, map[string]string{
		"url":      creds.URL,
		"host":     creds.Host,
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
