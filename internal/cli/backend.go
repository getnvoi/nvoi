package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// providerKinds is the single source of truth for the four provider kinds.
var providerKinds = []string{"compute", "dns", "storage", "build"}

func esc(s string) string { return url.PathEscape(s) }

// PathFunc builds a repo-scoped API path from a suffix.
type PathFunc = func(string) string

// ── Streaming ───────────────────────────────────────────────────────────────

// StreamRun POSTs a body and streams JSONL response through the TUI.
func StreamRun(client *APIClient, path string, body any) error {
	resp, err := client.DoRawWithBody("POST", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out := render.Resolve(false, false)
	var lastErr error
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		ev, err := app.ParseEvent(line)
		if err != nil {
			continue
		}
		if ev.Type == app.EventError {
			lastErr = fmt.Errorf("%s", ev.Message)
		}
		app.ReplayEvent(ev, out)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return lastErr
}

// ── Describe / Resources ────────────────────────────────────────────────────

// Describe fetches live cluster state from the API and renders it.
func Describe(client *APIClient, repoPath func(string) string, jsonOutput bool) error {
	var res app.DescribeResult
	if err := client.Do("GET", repoPath("/describe"), nil, &res); err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	render.RenderDescribe(&res)
	return nil
}

// Resources fetches all provider resources from the API and renders them.
func Resources(client *APIClient, repoPath func(string) string, jsonOutput bool) error {
	var groups []provider.ResourceGroup
	if err := client.Do("GET", repoPath("/resources"), nil, &groups); err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(groups)
	}
	render.RenderResources(groups)
	return nil
}

// ── Logs / Exec / SSH ───────────────────────────────────────────────────────

// LogsOpts holds flags for the logs command.
type LogsOpts struct {
	Service    string
	Follow     bool
	Tail       int
	Since      string
	Previous   bool
	Timestamps bool
}

// Logs streams service logs from the API.
func Logs(client *APIClient, repoPath func(string) string, opts LogsOpts) error {
	path := fmt.Sprintf("/services/%s/logs?tail=%d&since=%s", esc(opts.Service), opts.Tail, url.QueryEscape(opts.Since))
	if opts.Follow {
		path += "&follow=true"
	}
	if opts.Previous {
		path += "&previous=true"
	}
	if opts.Timestamps {
		path += "&timestamps=true"
	}
	resp, err := client.DoRaw("GET", repoPath(path))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

// Exec runs a command in a service pod via the API.
func Exec(client *APIClient, repoPath func(string) string, service string, command []string) error {
	resp, err := client.DoRawWithBody("POST", repoPath("/services/"+esc(service)+"/exec"), map[string]any{
		"command": command,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

// SSH runs a command on the master node via the API.
func SSH(client *APIClient, repoPath func(string) string, command []string) error {
	resp, err := client.DoRawWithBody("POST", repoPath("/ssh"), map[string]any{
		"command": command,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	return scanner.Err()
}

// ── Database ────────────────────────────────────────────────────────────────

// DatabaseBackupList lists backups via the API.
func DatabaseBackupList(client *APIClient, repoPath func(string) string, out app.Output, dbName string) error {
	var entries []struct {
		Key          string `json:"key"`
		Size         int64  `json:"size"`
		LastModified string `json:"last_modified"`
	}
	if err := client.Do("GET", repoPath(fmt.Sprintf("/database/backups?name=%s", url.QueryEscape(dbName))), nil, &entries); err != nil {
		return err
	}
	out.Command("database", "backup list", dbName)
	if len(entries) == 0 {
		out.Info("no backups found")
		return nil
	}
	for _, e := range entries {
		out.Info(fmt.Sprintf("%s  %s  %d bytes", e.LastModified, e.Key, e.Size))
	}
	return nil
}

// DatabaseBackupDownload downloads a backup via the API.
func DatabaseBackupDownload(client *APIClient, repoPath func(string) string, out app.Output, dbName, backupKey, outFile string) error {
	resp, err := client.DoRaw("GET", repoPath(fmt.Sprintf("/database/backups/%s?name=%s", esc(backupKey), url.QueryEscape(dbName))))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out.Command("database", "backup download", backupKey)

	var w io.Writer = os.Stdout
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return err
	}
	if outFile != "" {
		out.Success(fmt.Sprintf("downloaded %s (%d bytes)", outFile, n))
	}
	return nil
}

// DatabaseSQL runs a SQL query via the API.
// Unlike DatabaseBackupList/DatabaseBackupDownload, this prints raw query
// output directly — not through Output. psql/mysql table formatting breaks
// if wrapped in TUI chrome or JSONL events. --json has no effect. Intentional.
func DatabaseSQL(client *APIClient, repoPath func(string) string, dbName, query string) error {
	var result struct {
		Output string `json:"output"`
	}
	if err := client.Do("POST", repoPath("/database/sql"), map[string]any{
		"name":  dbName,
		"query": query,
	}, &result); err != nil {
		return err
	}
	fmt.Print(result.Output)
	return nil
}
