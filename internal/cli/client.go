package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const defaultAPIBase = "https://api.nvoi.to"

type APIClient struct {
	base  string
	token string
	http  *http.Client
}

func NewAPIClient(cfg *AuthConfig) *APIClient {
	base := cfg.APIBase
	if base == "" {
		base = defaultAPIBase
	}
	return &APIClient{
		base:  base,
		token: cfg.Token,
		http:  &http.Client{},
	}
}

// NewUnauthClient creates a client without a token — for login only.
func NewUnauthClient() *APIClient {
	base := os.Getenv("NVOI_API_BASE")
	if base == "" {
		base = defaultAPIBase
	}
	return &APIClient{
		base: base,
		http: &http.Client{},
	}
}

// authedClient loads auth config and returns an authenticated API client.
func authedClient() (*APIClient, *AuthConfig, error) {
	cfg, err := LoadAuthConfig()
	if err != nil {
		return nil, nil, err
	}
	return NewAPIClient(cfg), cfg, nil
}

func (c *APIClient) Do(method, path string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.base+path, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api error %d: %s", resp.StatusCode, string(respBody))
	}

	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// doRaw returns the raw HTTP response — caller must close the body.
// Used for streaming endpoints (JSONL logs).
func (c *APIClient) doRaw(method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("api error %d: %s", resp.StatusCode, string(respBody))
	}

	return resp, nil
}
