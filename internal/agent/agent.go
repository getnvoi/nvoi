// Package agent is the deploy runtime. It runs on the master node as a
// long-running HTTP server. It holds credentials, executes all operations,
// and streams JSONL results. The CLI and API are clients.
//
// Every endpoint returns JSONL. No JSON objects, no mixed formats.
// Handlers receive app.Output — they cannot write to http.ResponseWriter directly.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// AgentOpts holds values resolved at startup by cmd/ — the boundary
// where os.Getenv is legal. The agent never reads env vars directly.
type AgentOpts struct {
	SSHKey      []byte           // resolved SSH private key
	GitUsername string           // git auth username (e.g. "x-access-token")
	GitToken    string           // git auth token
	Kube        *kube.KubeClient // k8s client — direct to localhost:6443 on the master
	Token       string           // bearer token for agent auth — read from disk at startup
}

// agentState is the cached, immutable state swapped atomically on config push.
// Reads are lock-free. Writes (config push) build a new state and swap.
type agentState struct {
	cfg *config.AppConfig
	dc  *config.DeployContext // fully resolved, minus Output (stamped per-request)
}

// Agent is the deploy runtime. One per master node.
type Agent struct {
	state    atomic.Pointer[agentState] // lock-free reads, atomic swap on push
	opts     AgentOpts
	deployMu sync.Mutex // serializes deploy/teardown — reads don't block
}

// New creates an agent with the given config and pre-resolved options.
// Resolves credentials at startup — fails if secrets provider is unreachable.
func New(ctx context.Context, cfg *config.AppConfig, opts AgentOpts) (*Agent, error) {
	a := &Agent{opts: opts}
	if err := a.loadConfig(ctx, cfg); err != nil {
		return nil, err
	}
	return a, nil
}

// loadConfig builds a new agentState from config + credentials and swaps it in.
// Called at startup and on config push. Validates config, resolves credentials,
// builds DeployContext. If any step fails, the old state is preserved.
func (a *Agent) loadConfig(ctx context.Context, cfg *config.AppConfig) error {
	if err := reconcile.ValidateConfig(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	if err := cfg.Resolve(); err != nil {
		return fmt.Errorf("resolve config: %w", err)
	}
	dc, err := BuildDeployContext(ctx, nil, cfg, a.opts)
	if err != nil {
		return fmt.Errorf("build deploy context: %w", err)
	}
	a.state.Store(&agentState{cfg: cfg, dc: dc})
	return nil
}

// snapshot returns the current state and a per-request DeployContext with Output stamped.
func (a *Agent) snapshot(out app.Output) (*config.AppConfig, *config.DeployContext) {
	s := a.state.Load()
	// Shallow copy DeployContext so Output is per-request, rest is shared.
	dc := *s.dc
	dc.Output = out
	return s.cfg, &dc
}

// ── Handler type ────────────────────────────────────────────────────────────
// Every command handler receives Output and returns error. It cannot access
// http.ResponseWriter. JSONL is the only output path — enforced by the type.

// CommandFunc is a handler that produces output events. It receives
// *jsonlOutput — the agent's JSONL writer. It cannot access
// http.ResponseWriter. JSONL is the only output path.
type CommandFunc func(ctx context.Context, out *jsonlOutput, r *http.Request) error

// handle wraps a CommandFunc into an http.HandlerFunc. It creates the JSONL
// stream writer and calls the handler. The handler never sees w.
// Auth is checked before the handler runs.
func (a *Agent) handle(fn CommandFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		out := streamOutput(w)
		if err := fn(r.Context(), out, r); err != nil {
			out.Error(err)
		}
	}
}

// checkAuth validates the bearer token. Always passes if no token is configured
// (backwards compat with agents installed before token auth).
func (a *Agent) checkAuth(r *http.Request) bool {
	if a.opts.Token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	return h == "Bearer "+a.opts.Token
}

// RegisterRoutes wires all agent endpoints onto the mux.
func (a *Agent) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /deploy", a.handle(a.cmdDeploy))
	mux.HandleFunc("POST /teardown", a.handle(a.cmdTeardown))
	mux.HandleFunc("GET /describe", a.handle(a.cmdDescribe))
	mux.HandleFunc("GET /resources", a.handle(a.cmdResources))
	mux.HandleFunc("GET /logs/{service}", a.handle(a.cmdLogs))
	mux.HandleFunc("POST /exec/{service}", a.handle(a.cmdExec))
	mux.HandleFunc("POST /ssh", a.handle(a.cmdSSH))
	mux.HandleFunc("POST /cron/{name}/run", a.handle(a.cmdCronRun))
	mux.HandleFunc("GET /db/{name}/backups", a.handle(a.cmdDBBackupList))
	mux.HandleFunc("POST /db/{name}/sql", a.handle(a.cmdDBSQL))

	// Data endpoints — raw binary, not JSONL.
	mux.HandleFunc("GET /db/{name}/backups/{key...}", a.handleDBBackupDownload)

	// Control endpoints.
	mux.HandleFunc("POST /config", a.handleConfigPush)
	mux.HandleFunc("GET /health", a.handleHealth)
}

// ── Config push / Health ────────────────────────────────────────────────────

func (a *Agent) handleConfigPush(w http.ResponseWriter, r *http.Request) {
	if !a.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
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
	// Full validation + credential resolution + atomic swap.
	// If any step fails, the agent keeps its last good state.
	if err := a.loadConfig(r.Context(), cfg); err != nil {
		http.Error(w, fmt.Sprintf("config rejected: %v", err), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *Agent) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s := a.state.Load()
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"app":    s.cfg.App,
		"env":    s.cfg.Env,
	})
}

// ── Command handlers ────────────────────────────────────────────────────────
// Each handler receives Output only. JSONL is the only output path.

func (a *Agent) cmdDeploy(ctx context.Context, out *jsonlOutput, r *http.Request) error {
	a.deployMu.Lock()
	defer a.deployMu.Unlock()

	cfg, dc := a.snapshot(out)
	return reconcile.Deploy(ctx, dc, cfg)
}

func (a *Agent) cmdTeardown(ctx context.Context, out *jsonlOutput, r *http.Request) error {
	a.deployMu.Lock()
	defer a.deployMu.Unlock()

	var req struct {
		DeleteVolumes bool `json:"delete_volumes"`
		DeleteStorage bool `json:"delete_storage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}

	cfg, dc := a.snapshot(out)
	return core.Teardown(ctx, dc, cfg, req.DeleteVolumes, req.DeleteStorage)
}

func (a *Agent) cmdDescribe(ctx context.Context, out *jsonlOutput, _ *http.Request) error {
	cfg, dc := a.snapshot(out)
	res, err := app.Describe(ctx, app.DescribeRequest{
		Cluster: dc.Cluster, Output: dc.Output,
		StorageNames:   cfg.StorageNames(),
		ServiceSecrets: cfg.ServiceSecrets(),
	})
	if err != nil {
		return err
	}
	out.Data(app.NewDataEvent(res))
	return nil
}

func (a *Agent) cmdResources(ctx context.Context, out *jsonlOutput, _ *http.Request) error {
	_, dc := a.snapshot(out)
	groups, err := app.Resources(ctx, app.ResourcesRequest{
		Compute: app.ProviderRef{Name: dc.Cluster.Provider, Creds: dc.Cluster.Credentials},
		DNS:     dc.DNS,
		Storage: dc.Storage,
	})
	if err != nil {
		return err
	}
	out.Data(app.NewDataEvent(groups))
	return nil
}

func (a *Agent) cmdLogs(ctx context.Context, out *jsonlOutput, r *http.Request) error {
	service := r.PathValue("service")
	_, dc := a.snapshot(out)

	follow := r.URL.Query().Get("follow") == "true"
	since := r.URL.Query().Get("since")
	previous := r.URL.Query().Get("previous") == "true"
	timestamps := r.URL.Query().Get("timestamps") == "true"
	var tail int
	if t := r.URL.Query().Get("tail"); t != "" {
		fmt.Sscanf(t, "%d", &tail)
	}

	return app.Logs(ctx, app.LogsRequest{
		Cluster: dc.Cluster, Output: dc.Output, Service: service,
		Follow: follow, Tail: tail, Since: since,
		Previous: previous, Timestamps: timestamps,
	})
}

func (a *Agent) cmdExec(ctx context.Context, out *jsonlOutput, r *http.Request) error {
	service := r.PathValue("service")
	var req struct {
		Command []string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}

	_, dc := a.snapshot(out)
	return app.Exec(ctx, app.ExecRequest{
		Cluster: dc.Cluster, Output: dc.Output, Service: service, Command: req.Command,
	})
}

func (a *Agent) cmdSSH(ctx context.Context, out *jsonlOutput, r *http.Request) error {
	var req struct {
		Command []string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}

	_, dc := a.snapshot(out)
	return app.SSH(ctx, app.SSHRequest{
		Cluster: dc.Cluster, Output: dc.Output, Command: req.Command,
	})
}

func (a *Agent) cmdCronRun(ctx context.Context, out *jsonlOutput, r *http.Request) error {
	name := r.PathValue("name")
	_, dc := a.snapshot(out)
	return app.CronRun(ctx, app.CronRunRequest{
		Cluster: dc.Cluster, Output: dc.Output, Name: name,
	})
}

func (a *Agent) cmdDBBackupList(ctx context.Context, out *jsonlOutput, r *http.Request) error {
	dbName := r.PathValue("name")
	cfg, dc := a.snapshot(out)

	name, err := utils.ResolveDBName(dbName, cfg.DatabaseNames())
	if err != nil {
		return err
	}
	entries, err := app.DatabaseBackupList(ctx, app.DatabaseBackupListRequest{
		Cluster: dc.Cluster, Output: dc.Output, DBName: name,
	})
	if err != nil {
		return err
	}
	out.Data(app.NewDataEvent(entries))
	return nil
}

func (a *Agent) cmdDBSQL(ctx context.Context, out *jsonlOutput, r *http.Request) error {
	dbName := r.PathValue("name")
	var req struct {
		Engine string `json:"engine"`
		Query  string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}

	cfg, dc := a.snapshot(out)

	name, err := utils.ResolveDBName(dbName, cfg.DatabaseNames())
	if err != nil {
		return err
	}
	engine := req.Engine
	if engine == "" {
		if db, ok := cfg.Database[name]; ok {
			engine = db.Kind
		}
	}
	if engine == "" {
		return fmt.Errorf("engine is required (postgres or mysql)")
	}
	output, err := app.DatabaseSQL(ctx, app.DatabaseSQLRequest{
		Cluster: dc.Cluster, Output: dc.Output, DBName: name, Engine: engine, Query: req.Query,
	})
	if err != nil {
		return err
	}
	out.Data(app.NewDataEvent(output))
	return nil
}

// ── Binary data endpoint ────────────────────────────────────────────────────
// Backup download is raw binary — not JSONL.

func (a *Agent) handleDBBackupDownload(w http.ResponseWriter, r *http.Request) {
	if !a.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dbName := r.PathValue("name")
	key := r.PathValue("key")

	cfg, dc := a.snapshot(render.NewJSONOutput(w))

	name, err := utils.ResolveDBName(dbName, cfg.DatabaseNames())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, _, err := app.DatabaseBackupDownload(r.Context(), app.DatabaseBackupDownloadRequest{
		Cluster: dc.Cluster, Output: dc.Output, DBName: name, Key: key,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, body)
}

// ── Streaming output ────────────────────────────────────────────────────────

// streamOutput creates a JSONL output that writes directly to the HTTP
// response with flushing. Each event is one JSONL line, delivered immediately.
func streamOutput(w http.ResponseWriter) *jsonlOutput {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return &jsonlOutput{enc: json.NewEncoder(flushWriter{w})}
}

// jsonlOutput implements app.Output by writing JSONL events.
type jsonlOutput struct {
	enc *json.Encoder
}

func (j *jsonlOutput) Command(command, action, name string, extra ...any) {
	j.enc.Encode(app.NewCommandEvent(command, action, name, extra...))
}
func (j *jsonlOutput) Progress(msg string) { j.enc.Encode(app.NewMessageEvent(app.EventProgress, msg)) }
func (j *jsonlOutput) Success(msg string)  { j.enc.Encode(app.NewMessageEvent(app.EventSuccess, msg)) }
func (j *jsonlOutput) Warning(msg string)  { j.enc.Encode(app.NewMessageEvent(app.EventWarning, msg)) }
func (j *jsonlOutput) Info(msg string)     { j.enc.Encode(app.NewMessageEvent(app.EventInfo, msg)) }
func (j *jsonlOutput) Error(err error) {
	j.enc.Encode(app.NewMessageEvent(app.EventError, err.Error()))
}
func (j *jsonlOutput) Writer() io.Writer {
	return &streamWriter{enc: j.enc}
}
func (j *jsonlOutput) Data(ev app.Event) {
	j.enc.Encode(ev)
}

// streamWriter wraps Output.Writer() to emit each line as a stream event.
type streamWriter struct {
	enc *json.Encoder
}

func (sw *streamWriter) Write(p []byte) (int, error) {
	sw.enc.Encode(app.NewMessageEvent(app.EventStream, string(p)))
	return len(p), nil
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
