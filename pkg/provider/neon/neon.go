package neon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
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
		client:  &http.Client{},
	}
}

func (p *Provider) Close() error { return nil }
func (p *Provider) ListResources(context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}

func (p *Provider) ValidateCredentials(ctx context.Context) error {
	_, err := p.request(ctx, http.MethodGet, "/projects", nil)
	return err
}

func (p *Provider) EnsureCredentials(ctx context.Context, kc *kube.Client, req provider.DatabaseRequest) (provider.DatabaseCredentials, error) {
	if kc != nil {
		if url, err := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "url"); err == nil && url != "" {
			host, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "host")
			user, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "user")
			password, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "password")
			database, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "database")
			return provider.DatabaseCredentials{URL: url, Host: host, User: user, Password: password, Database: database, Port: 5432, SSLMode: "require"}, nil
		}
	}

	project := map[string]any{"name": req.FullName, "region": req.Spec.Region}
	resp, err := p.request(ctx, http.MethodPost, "/projects", project)
	if err != nil {
		return provider.DatabaseCredentials{}, err
	}
	var created struct {
		Host     string `json:"host"`
		User     string `json:"user"`
		Password string `json:"password"`
		Database string `json:"database"`
		URL      string `json:"url"`
	}
	if err := json.Unmarshal(resp, &created); err != nil {
		return provider.DatabaseCredentials{}, err
	}
	creds := provider.DatabaseCredentials{
		URL:      created.URL,
		Host:     created.Host,
		Port:     5432,
		User:     created.User,
		Password: created.Password,
		Database: created.Database,
		SSLMode:  "require",
	}
	if kc != nil {
		_ = kc.EnsureSecret(ctx, req.Namespace, req.CredentialsSecretName, map[string]string{
			"url":      creds.URL,
			"host":     creds.Host,
			"user":     creds.User,
			"password": creds.Password,
			"database": creds.Database,
			"sslmode":  creds.SSLMode,
		})
	}
	return creds, nil
}

func (p *Provider) Reconcile(context.Context, provider.DatabaseRequest) (*provider.DatabasePlan, error) {
	return &provider.DatabasePlan{}, nil
}

func (p *Provider) Delete(ctx context.Context, req provider.DatabaseRequest) error {
	_, err := p.request(ctx, http.MethodDelete, "/projects/"+req.FullName, nil)
	return err
}

func (p *Provider) ExecSQL(ctx context.Context, req provider.DatabaseRequest, stmt string) (*provider.SQLResult, error) {
	resp, err := p.request(ctx, http.MethodPost, "/sql", map[string]any{"project": req.FullName, "query": stmt})
	if err != nil {
		return nil, err
	}
	var out provider.SQLResult
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Provider) BackupNow(ctx context.Context, req provider.DatabaseRequest) (*provider.BackupRef, error) {
	resp, err := p.request(ctx, http.MethodPost, "/projects/"+req.FullName+"/branches", map[string]any{"name": "backup-now"})
	if err != nil {
		return nil, err
	}
	var ref provider.BackupRef
	if err := json.Unmarshal(resp, &ref); err != nil {
		return nil, err
	}
	return &ref, nil
}

func (p *Provider) ListBackups(ctx context.Context, req provider.DatabaseRequest) ([]provider.BackupRef, error) {
	resp, err := p.request(ctx, http.MethodGet, "/projects/"+req.FullName+"/branches", nil)
	if err != nil {
		return nil, err
	}
	var refs []provider.BackupRef
	if err := json.Unmarshal(resp, &refs); err != nil {
		return nil, err
	}
	return refs, nil
}

func (p *Provider) DownloadBackup(ctx context.Context, req provider.DatabaseRequest, backupID string, w io.Writer) error {
	resp, err := p.request(ctx, http.MethodGet, "/projects/"+req.FullName+"/branches/"+backupID+"/dump", nil)
	if err != nil {
		return err
	}
	_, err = w.Write(resp)
	return err
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
		return nil, fmt.Errorf("neon API %s %s: %s", method, path, strings.TrimSpace(string(b)))
	}
	return b, nil
}
