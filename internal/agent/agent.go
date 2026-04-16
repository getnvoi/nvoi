// Package agent is the deploy runtime. It runs on the master node as a
// long-running HTTP server. It holds credentials, executes all operations,
// and streams JSONL results. The CLI and API are clients.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Agent is the deploy runtime. One per master node.
type Agent struct {
	ctx context.Context
	cfg *config.AppConfig
	mu  sync.Mutex // serialize deploys — one at a time
}

// New creates an agent with the given config.
func New(ctx context.Context, cfg *config.AppConfig) *Agent {
	return &Agent{ctx: ctx, cfg: cfg}
}

// RegisterRoutes wires all agent endpoints onto the mux.
func (a *Agent) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /deploy", a.handleDeploy)
	mux.HandleFunc("POST /teardown", a.handleTeardown)
	mux.HandleFunc("GET /describe", a.handleDescribe)
	mux.HandleFunc("GET /resources", a.handleResources)
	mux.HandleFunc("GET /logs/{service}", a.handleLogs)
	mux.HandleFunc("POST /exec/{service}", a.handleExec)
	mux.HandleFunc("POST /ssh", a.handleSSH)
	mux.HandleFunc("POST /cron/{name}/run", a.handleCronRun)
	mux.HandleFunc("GET /db/{name}/backups", a.handleDBBackupList)
	mux.HandleFunc("GET /db/{name}/backups/{key...}", a.handleDBBackupDownload)
	mux.HandleFunc("POST /db/{name}/sql", a.handleDBSQL)
	mux.HandleFunc("POST /config", a.handleConfigPush)
	mux.HandleFunc("GET /health", a.handleHealth)
}

// ── Config push ─────────────────────────────────────────────────────────────
// The CLI pushes nvoi.yaml before each deploy. The agent reloads.

func (a *Agent) handleConfigPush(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := config.ParseAppConfig(data)
	if err != nil {
		http.Error(w, fmt.Sprintf("parse config: %v", err), http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (a *Agent) handleHealth(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"app":    cfg.App,
		"env":    cfg.Env,
	})
}

// ── Deploy / Teardown ───────────────────────────────────────────────────────

func (a *Agent) handleDeploy(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, a.cfg)

	if err := reconcile.Deploy(r.Context(), dc, a.cfg); err != nil {
		out.Error(err)
	}
}

func (a *Agent) handleTeardown(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var req struct {
		DeleteVolumes bool `json:"delete_volumes"`
		DeleteStorage bool `json:"delete_storage"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, a.cfg)

	if err := core.Teardown(r.Context(), dc, a.cfg, req.DeleteVolumes, req.DeleteStorage); err != nil {
		out.Error(err)
	}
}

// ── Describe / Resources ────────────────────────────────────────────────────

func (a *Agent) handleDescribe(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, cfg)

	raw, err := app.DescribeJSON(r.Context(), app.DescribeRequest{
		Cluster:        dc.Cluster,
		StorageNames:   cfg.StorageNames(),
		ServiceSecrets: cfg.ServiceSecrets(),
	})
	if err != nil {
		out.Error(err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(raw)
}

func (a *Agent) handleResources(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, cfg)

	groups, err := app.Resources(r.Context(), app.ResourcesRequest{
		Compute: app.ProviderRef{Name: dc.Cluster.Provider, Creds: dc.Cluster.Credentials},
		DNS:     dc.DNS,
		Storage: dc.Storage,
	})
	if err != nil {
		out.Error(err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(groups)
}

// ── Logs / Exec / SSH ───────────────────────────────────────────────────────

func (a *Agent) handleLogs(w http.ResponseWriter, r *http.Request) {
	service := r.PathValue("service")
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, cfg)

	follow := r.URL.Query().Get("follow") == "true"
	since := r.URL.Query().Get("since")
	previous := r.URL.Query().Get("previous") == "true"
	timestamps := r.URL.Query().Get("timestamps") == "true"
	var tail int
	if t := r.URL.Query().Get("tail"); t != "" {
		fmt.Sscanf(t, "%d", &tail)
	}

	if err := app.Logs(r.Context(), app.LogsRequest{
		Cluster: dc.Cluster, Service: service,
		Follow: follow, Tail: tail, Since: since,
		Previous: previous, Timestamps: timestamps,
	}); err != nil {
		out.Error(err)
	}
}

func (a *Agent) handleExec(w http.ResponseWriter, r *http.Request) {
	service := r.PathValue("service")
	var req struct {
		Command []string `json:"command"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, cfg)

	if err := app.Exec(r.Context(), app.ExecRequest{
		Cluster: dc.Cluster, Service: service, Command: req.Command,
	}); err != nil {
		out.Error(err)
	}
}

func (a *Agent) handleSSH(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command []string `json:"command"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, cfg)

	if err := app.SSH(r.Context(), app.SSHRequest{
		Cluster: dc.Cluster, Command: req.Command,
	}); err != nil {
		out.Error(err)
	}
}

// ── Crons ───────────────────────────────────────────────────────────────────

func (a *Agent) handleCronRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, cfg)

	if err := app.CronRun(r.Context(), app.CronRunRequest{
		Cluster: dc.Cluster, Name: name,
	}); err != nil {
		out.Error(err)
	}
}

// ── Database ────────────────────────────────────────────────────────────────

func (a *Agent) handleDBBackupList(w http.ResponseWriter, r *http.Request) {
	dbName := r.PathValue("name")
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, cfg)

	name, err := utils.ResolveDBName(dbName, cfg.DatabaseNames())
	if err != nil {
		out.Error(err)
		return
	}
	entries, err := app.DatabaseBackupList(r.Context(), app.DatabaseBackupListRequest{
		Cluster: dc.Cluster, DBName: name,
	})
	if err != nil {
		out.Error(err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (a *Agent) handleDBBackupDownload(w http.ResponseWriter, r *http.Request) {
	dbName := r.PathValue("name")
	key := r.PathValue("key")

	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	dc := BuildDeployContext(r.Context(), render.NewJSONOutput(w), cfg)

	name, err := utils.ResolveDBName(dbName, cfg.DatabaseNames())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, _, err := app.DatabaseBackupDownload(r.Context(), app.DatabaseBackupDownloadRequest{
		Cluster: dc.Cluster, DBName: name, Key: key,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, body)
}

func (a *Agent) handleDBSQL(w http.ResponseWriter, r *http.Request) {
	dbName := r.PathValue("name")
	var req struct {
		Engine string `json:"engine"`
		Query  string `json:"query"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	out := streamOutput(w)
	dc := BuildDeployContext(r.Context(), out, cfg)

	name, err := utils.ResolveDBName(dbName, cfg.DatabaseNames())
	if err != nil {
		out.Error(err)
		return
	}
	engine := req.Engine
	if engine == "" {
		if db, ok := cfg.Database[name]; ok {
			engine = db.Kind
		}
	}
	if engine == "" {
		out.Error(fmt.Errorf("engine is required (postgres or mysql)"))
		return
	}
	output, err := app.DatabaseSQL(r.Context(), app.DatabaseSQLRequest{
		Cluster: dc.Cluster, DBName: name, Engine: engine, Query: req.Query,
	})
	if err != nil {
		out.Error(err)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, output)
}

// ── Streaming output ────────────────────────────────────────────────────────

// streamOutput creates a JSONL output that writes directly to the HTTP
// response with flushing. Each event is one JSONL line, delivered immediately.
func streamOutput(w http.ResponseWriter) app.Output {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return render.NewJSONOutput(flushWriter{w})
}

// flushWriter wraps an http.ResponseWriter to flush after every Write.
type flushWriter struct {
	w http.ResponseWriter
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if f, ok := fw.w.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}
