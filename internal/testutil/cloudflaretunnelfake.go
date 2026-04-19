package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/cloudflare"
)

// CloudflareTunnelFake is a stateful in-memory Cloudflare Tunnel API.
// A real cloudflare/tunnel.Client registered via Register() talks to this
// fake over the wire — same httptest.Server approach as HetznerFake.
type CloudflareTunnelFake struct {
	*httptest.Server
	*OpLog

	mu      sync.Mutex
	seqID   int
	tunnels map[string]*cfFakeTunnel // id → tunnel
}

type cfFakeTunnel struct {
	ID     string
	Name   string
	Token  string
	Config map[string]any
}

// NewCloudflareTunnelFake creates a running CF Tunnel fake.
func NewCloudflareTunnelFake(t *testing.T) *CloudflareTunnelFake {
	f := &CloudflareTunnelFake{
		OpLog:   NewOpLog(),
		tunnels: map[string]*cfFakeTunnel{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() { f.Server.Close() })
	return f
}

// Register binds this fake to the tunnel registry under name.
func (f *CloudflareTunnelFake) Register(name string) {
	schema := provider.CredentialSchema{Name: name}
	provider.RegisterTunnel(name, schema, func(creds map[string]string) provider.TunnelProvider {
		c := cloudflare.NewClient(map[string]string{
			"api_token":  "test-token",
			"account_id": "test-account",
		})
		c.APIClient().BaseURL = f.URL
		return c
	})
}

// SeedTunnel inserts a tunnel into the fake.
func (f *CloudflareTunnelFake) SeedTunnel(id, name, token string) *cfFakeTunnel {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &cfFakeTunnel{ID: id, Name: name, Token: token}
	f.tunnels[id] = t
	return t
}

var (
	reCFTunnelToken  = regexp.MustCompile(`^/accounts/([^/]+)/cfd_tunnel/([^/]+)/token$`)
	reCFTunnelConfig = regexp.MustCompile(`^/accounts/([^/]+)/cfd_tunnel/([^/]+)/configurations$`)
	reCFTunnelByID   = regexp.MustCompile(`^/accounts/([^/]+)/cfd_tunnel/([^/]+)$`)
	reCFTunnelList   = regexp.MustCompile(`^/accounts/([^/]+)/cfd_tunnel$`)
)

func (f *CloudflareTunnelFake) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if reCFTunnelToken.MatchString(path) {
		m := reCFTunnelToken.FindStringSubmatch(path)
		f.handleToken(w, m[2])
		return
	}
	if reCFTunnelConfig.MatchString(path) {
		m := reCFTunnelConfig.FindStringSubmatch(path)
		f.handleConfig(w, r, m[2])
		return
	}
	if m := reCFTunnelByID.FindStringSubmatch(path); m != nil {
		switch r.Method {
		case "DELETE":
			f.handleDelete(w, m[2])
		case "GET":
			f.handleGet(w, m[2])
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	if reCFTunnelList.MatchString(path) {
		switch r.Method {
		case "GET":
			f.handleList(w, r)
		case "POST":
			f.handleCreate(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	http.Error(w, "not found", 404)
}

func (f *CloudflareTunnelFake) handleList(w http.ResponseWriter, r *http.Request) {
	// Invariant: every list MUST include is_deleted=false.
	if r.URL.Query().Get("is_deleted") != "false" {
		w.WriteHeader(400)
		writeCFTunnelError(w, 400, "is_deleted=false is required on tunnel lookups")
		return
	}
	name := r.URL.Query().Get("name")
	_ = f.Record("find-tunnel:" + name)

	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0)
	for _, t := range f.tunnels {
		if name != "" && t.Name != name {
			continue
		}
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "status": "active"})
	}
	writeCFTunnelOK(w, out)
}

func (f *CloudflareTunnelFake) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		ConfigSrc string `json:"config_src"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := f.Record("create-tunnel:" + body.Name); err != nil {
		writeCFTunnelError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	f.seqID++
	id := fmt.Sprintf("tunnel-%d", f.seqID)
	t := &cfFakeTunnel{ID: id, Name: body.Name, Token: "tok-" + id}
	f.tunnels[id] = t
	f.mu.Unlock()
	writeCFTunnelOK(w, map[string]any{"id": id, "name": body.Name, "status": "active"})
}

func (f *CloudflareTunnelFake) handleGet(w http.ResponseWriter, id string) {
	f.mu.Lock()
	t, ok := f.tunnels[id]
	f.mu.Unlock()
	if !ok {
		writeCFTunnelError(w, 404, "tunnel not found")
		return
	}
	writeCFTunnelOK(w, map[string]any{"id": t.ID, "name": t.Name, "status": "active"})
}

func (f *CloudflareTunnelFake) handleToken(w http.ResponseWriter, id string) {
	_ = f.Record("token:" + id)
	f.mu.Lock()
	t, ok := f.tunnels[id]
	f.mu.Unlock()
	if !ok {
		writeCFTunnelError(w, 404, "tunnel not found")
		return
	}
	writeCFTunnelOK(w, t.Token)
}

func (f *CloudflareTunnelFake) handleConfig(w http.ResponseWriter, r *http.Request, id string) {
	_ = f.Record("config:" + id)
	f.mu.Lock()
	t, ok := f.tunnels[id]
	if ok {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		t.Config = body
	}
	f.mu.Unlock()
	if !ok {
		writeCFTunnelError(w, 404, "tunnel not found")
		return
	}
	writeCFTunnelOK(w, map[string]any{"tunnel_id": id})
}

func (f *CloudflareTunnelFake) handleDelete(w http.ResponseWriter, id string) {
	_ = f.Record("delete-tunnel:" + id)
	f.mu.Lock()
	_, ok := f.tunnels[id]
	if ok {
		delete(f.tunnels, id)
	}
	f.mu.Unlock()
	if !ok {
		writeCFTunnelError(w, 404, "tunnel not found")
		return
	}
	writeCFTunnelOK(w, nil)
}

func writeCFTunnelOK(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "success": true})
}

func writeCFTunnelError(w http.ResponseWriter, status int, msg string) {
	if status != 200 {
		w.WriteHeader(status)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"errors":  []map[string]any{{"code": status, "message": msg}},
	})
}

// HasTunnel returns true if a tunnel with the given name exists in the fake.
func (f *CloudflareTunnelFake) HasTunnel(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tunnels {
		if t.Name == name {
			return true
		}
	}
	return false
}

// TunnelCount returns the number of tunnels in the fake.
func (f *CloudflareTunnelFake) TunnelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tunnels)
}

// GetTunnelConfig returns the last pushed configuration for a tunnel ID.
func (f *CloudflareTunnelFake) GetTunnelConfig(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tunnels[id]; ok {
		return t.Config
	}
	return nil
}

// TunnelIDByName returns the ID of a tunnel by name, or "" if not found.
func (f *CloudflareTunnelFake) TunnelIDByName(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tunnels {
		if t.Name == name {
			return t.ID
		}
	}
	return ""
}

// ensure no unused import for strings
var _ = strings.HasPrefix
