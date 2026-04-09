package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const defaultAPIBase = "https://api.nvoi.to"

// APIError is a typed error from the API with a status code.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api error %d: %s", e.Status, e.Message)
}

// IsNotFound returns true if the error is a 404 from the API.
func IsNotFound(err error) bool {
	if e, ok := err.(*APIError); ok {
		return e.Status == 404
	}
	return false
}

type APIClient struct {
	base   string
	token  string
	http   *http.Client
	stream *http.Client // no timeout — for streaming endpoints (/run, logs, ssh)
}

func NewAPIClient(cfg *AuthConfig) *APIClient {
	base := cfg.APIBase
	if base == "" {
		base = defaultAPIBase
	}
	return &APIClient{
		base:   base,
		token:  cfg.Token,
		http:   &http.Client{Timeout: 30 * time.Second},
		stream: &http.Client{},
	}
}

// NewUnauthClient creates a client without a token — for login only.
func NewUnauthClient() *APIClient {
	base := os.Getenv("NVOI_API_BASE")
	if base == "" {
		base = defaultAPIBase
	}
	return &APIClient{
		base:   base,
		http:   &http.Client{Timeout: 30 * time.Second},
		stream: &http.Client{},
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
		return parseAPIError(resp.StatusCode, respBody)
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

	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, parseAPIError(resp.StatusCode, respBody)
	}

	return resp, nil
}

// doRawWithBody sends a request with a JSON body and returns the raw response.
// Caller must close the body. Used for streaming POST endpoints (/run, SSH).
func (c *APIClient) doRawWithBody(method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.base+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, parseAPIError(resp.StatusCode, respBody)
	}

	return resp, nil
}

func parseAPIError(status int, body []byte) *APIError {
	// Try our app format: {"error": "..."}
	var appErr struct {
		Err string `json:"error"`
	}
	if json.Unmarshal(body, &appErr) == nil && appErr.Err != "" {
		return &APIError{Status: status, Message: appErr.Err}
	}

	// Try Huma validation format: {"title": "...", "detail": "...", "errors": [...]}
	var humaErr struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Errors []struct {
			Message  string `json:"message"`
			Location string `json:"location"`
			Value    any    `json:"value"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &humaErr) == nil && len(humaErr.Errors) > 0 {
		parts := make([]string, len(humaErr.Errors))
		for i, e := range humaErr.Errors {
			if e.Location != "" {
				parts[i] = e.Location + ": " + e.Message
			} else {
				parts[i] = e.Message
			}
		}
		msg := humaErr.Detail
		if msg == "" {
			msg = humaErr.Title
		}
		return &APIError{Status: status, Message: msg + ": " + fmt.Sprintf("%s", joinStrings(parts))}
	}
	if json.Unmarshal(body, &humaErr) == nil && humaErr.Detail != "" {
		return &APIError{Status: status, Message: humaErr.Detail}
	}

	return &APIError{Status: status, Message: string(body)}
}

func joinStrings(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "; "
		}
		result += p
	}
	return result
}
