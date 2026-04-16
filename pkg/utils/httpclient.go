// Package utils — httpclient provides a shared JSON-over-HTTP client for provider APIs.
package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultHTTPClient is used when HTTPClient.HTTPClient is nil.
// 30s timeout prevents hanging on unresponsive APIs.
var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

type HTTPClient struct {
	BaseURL    string
	HTTPClient *http.Client
	SetAuth    func(*http.Request)
	Label      string
}

func (c *HTTPClient) Do(ctx context.Context, method, path string, body, result any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
	if err != nil {
		return err
	}
	if c.SetAuth != nil {
		c.SetAuth(req)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := c.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read response: %w", c.Label, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Body: string(respBody), label: c.Label}
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("%s: decode response: %w", c.Label, err)
		}
	}
	return nil
}

type APIError struct {
	Status int
	Body   string
	label  string
}

func (e *APIError) Error() string {
	// Try to extract a human-readable message from JSON error responses.
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(e.Body), &parsed) == nil && parsed.Error.Message != "" {
		return fmt.Sprintf("%s: %s (%s)", e.label, parsed.Error.Message, parsed.Error.Code)
	}
	return fmt.Sprintf("%s: %d %s", e.label, e.Status, e.Body)
}
func (e *APIError) HTTPStatus() int { return e.Status }

// ErrNotFound indicates the resource didn't exist.
// Returned by delete operations on 404. Callers decide how to present it.
var ErrNotFound = fmt.Errorf("not found")

func IsNotFound(err error) bool {
	if errors.Is(err, ErrNotFound) {
		return true
	}
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.Status == 404
	}
	return false
}

// IsConflict returns true if the error is an HTTP 409 Conflict.
func IsConflict(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.Status == 409
	}
	return false
}
