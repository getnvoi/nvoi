package cli

import (
	"bufio"
	"fmt"
	"net/url"

	"github.com/getnvoi/nvoi/internal/render"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

// providerKinds is the single source of truth for the four provider kinds.
var providerKinds = []string{"compute", "dns", "storage", "build"}

func esc(s string) string { return url.PathEscape(s) }

// PathFunc builds a repo-scoped API path from a suffix.
type PathFunc = func(string) string

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
		ev, err := pkgcore.ParseEvent(line)
		if err != nil {
			continue
		}
		if ev.Type == pkgcore.EventError {
			lastErr = fmt.Errorf("%s", ev.Message)
		}
		pkgcore.ReplayEvent(ev, out)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return lastErr
}
