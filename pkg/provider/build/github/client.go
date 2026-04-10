package github

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/crypto/nacl/box"
)

const apiBase = "https://api.github.com"

type githubClient struct {
	token string
	http  *http.Client
}

func newClient(token string) *githubClient {
	return &githubClient{
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

// ── Secrets ───────────────────────────────────────────────────────────────────

// EnsureSecret creates or updates a repo secret using libsodium sealed box encryption.
func (c *githubClient) EnsureSecret(ctx context.Context, owner, repo, name, value string) error {
	// 1. Get repo public key
	var pubKeyResp struct {
		KeyID string `json:"key_id"`
		Key   string `json:"key"`
	}
	if err := c.get(ctx, fmt.Sprintf("/repos/%s/%s/actions/secrets/public-key", owner, repo), &pubKeyResp); err != nil {
		return fmt.Errorf("get repo public key: %w", err)
	}

	// 2. Decrypt the base64 public key
	pubKeyBytes, err := base64.StdEncoding.DecodeString(pubKeyResp.Key)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	var recipientKey [32]byte
	copy(recipientKey[:], pubKeyBytes)

	// 3. Encrypt with libsodium sealed box (nacl/box.SealAnonymous)
	encrypted, err := box.SealAnonymous(nil, []byte(value), &recipientKey, rand.Reader)
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}

	// 4. PUT the encrypted secret
	body := map[string]string{
		"encrypted_value": base64.StdEncoding.EncodeToString(encrypted),
		"key_id":          pubKeyResp.KeyID,
	}
	return c.put(ctx, fmt.Sprintf("/repos/%s/%s/actions/secrets/%s", owner, repo, name), body)
}

// ── Workflow file ─────────────────────────────────────────────────────────────

// EnsureWorkflow creates or updates the workflow file in the repo.
func (c *githubClient) EnsureWorkflow(ctx context.Context, owner, repo, path, content string) error {
	// Check if file exists
	var existing struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	err := c.get(ctx, fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, path), &existing)

	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	if err == nil {
		// File exists — check if content matches
		existingContent, _ := base64.StdEncoding.DecodeString(existing.Content)
		if string(existingContent) == content {
			return nil // already up to date
		}
		// Update
		body := map[string]string{
			"message": "nvoi: update build workflow",
			"content": encoded,
			"sha":     existing.SHA,
		}
		return c.put(ctx, fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, path), body)
	}

	// Create
	body := map[string]string{
		"message": "nvoi: add build workflow",
		"content": encoded,
	}
	return c.put(ctx, fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, path), body)
}

// ── Workflow dispatch ─────────────────────────────────────────────────────────

// TriggerWorkflow dispatches the workflow and returns the run ID.
func (c *githubClient) TriggerWorkflow(ctx context.Context, owner, repo, workflowFile string, inputs map[string]string) (int64, error) {
	// Record time before trigger
	before := time.Now().UTC()

	// Dispatch
	body := map[string]interface{}{
		"ref":    "main",
		"inputs": inputs,
	}
	// GitHub API wants just the filename, not the full path
	filename := workflowFile
	if idx := len(filename) - 1; idx >= 0 {
		for i := len(filename) - 1; i >= 0; i-- {
			if filename[i] == '/' {
				filename = filename[i+1:]
				break
			}
		}
	}
	if err := c.post(ctx, fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/dispatches", owner, repo, filename), body); err != nil {
		return 0, fmt.Errorf("dispatch workflow: %w", err)
	}

	// Poll for the new run (dispatch API returns 204, no run ID)
	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)

		var runs struct {
			WorkflowRuns []struct {
				ID        int64     `json:"id"`
				CreatedAt time.Time `json:"created_at"`
				Status    string    `json:"status"`
			} `json:"workflow_runs"`
		}
		if err := c.get(ctx, fmt.Sprintf("/repos/%s/%s/actions/runs?event=workflow_dispatch&per_page=5", owner, repo), &runs); err != nil {
			continue
		}
		for _, run := range runs.WorkflowRuns {
			if run.CreatedAt.After(before.Add(-5 * time.Second)) {
				return run.ID, nil
			}
		}
	}
	return 0, fmt.Errorf("timed out waiting for workflow run to appear")
}

// ── Run polling ───────────────────────────────────────────────────────────────

// WaitForRun polls until the run completes.
func (c *githubClient) WaitForRun(ctx context.Context, owner, repo string, runID int64, stdout io.Writer) error {
	url := fmt.Sprintf("/repos/%s/%s/actions/runs/%d", owner, repo, runID)
	runURL := fmt.Sprintf("https://github.com/%s/%s/actions/runs/%d", owner, repo, runID)

	lastStatus := ""
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var run struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		}
		if err := c.get(ctx, url, &run); err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		if run.Status != lastStatus {
			fmt.Fprintf(stdout, "  [%s]\n", run.Status)
			lastStatus = run.Status
		}

		if run.Status == "completed" {
			if run.Conclusion == "success" {
				return nil
			}
			return fmt.Errorf("workflow run failed (conclusion: %s)\n  → %s", run.Conclusion, runURL)
		}

		time.Sleep(5 * time.Second)
	}
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *githubClient) get(ctx context.Context, path string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not found: %s", path)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github api %s: %d %s", path, resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *githubClient) put(ctx context.Context, path string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiBase+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github api PUT %s: %d %s", path, resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *githubClient) post(ctx context.Context, path string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 204 No Content is success for dispatch
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github api POST %s: %d %s", path, resp.StatusCode, string(respBody))
	}
	return nil
}
