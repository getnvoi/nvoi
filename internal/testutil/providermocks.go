// Package testutil — provider mocks.
//
// ─────────────────────────────────────────────────────────────────────────────
// GOVERNANCE — READ BEFORE EDITING OR ADDING A MOCK
// ─────────────────────────────────────────────────────────────────────────────
//
// Single pattern, single file. Every provider-boundary mock in the nvoi test
// suite lives here and follows the same shape.
//
// Rules (hard):
//
//   1. One file. This one. Nothing provider-mock-shaped lives anywhere else.
//
//   2. No test declares a type that implements ComputeProvider / DNSProvider /
//      BucketProvider. Ever. The real provider clients (hetzner.Client,
//      cloudflare.DNSClient, cloudflare.Client) are exercised end-to-end
//      against httptest.Server. If you're tempted to write `type myMock
//      struct { ... }` in a _test.go file, stop — extend the fake here.
//
//   3. Tests seed state (SeedServer, SeedVolume, SeedFirewall, SeedNetwork,
//      SeedDNSRecord, SeedBucket). Tests never stub behavior (no func hooks
//      that replace a handler branch). If a new behavior is needed, it lives
//      in the fake's HTTP handler — one place.
//
//   4. The OpLog is the assertion surface. Tests call fake.Has / fake.Count /
//      fake.IndexOf / fake.All. Tests do not poke HTTP request history
//      directly. If a new op is needed, the handler records it.
//
//   5. Error injection is explicit: ErrorOn("delete-firewall:<name>", err).
//      The matching HTTP handler short-circuits to a 500 with the error
//      message. No `if testMode then ...` branches anywhere.
//
//   6. Register binds the fake to a named provider in the global registry.
//      The factory constructs a real provider client and points it at the
//      fake's URL. One line in each test.
//
//   7. MockSSH (pkg-level, internal/testutil/mock_ssh.go) and
//      kubefake.KubeFake stay. Those are at correct boundaries (SSH protocol,
//      client-go fake). Do NOT add SSH or Kube fakes here.
//
//   8. MockOutput stays in mock_provider.go — it's an internal UI contract,
//      not an external boundary.
//
//   9. If you feel the urge to add a parallel fake variant "because the test
//      is different" — it's not. Extend this fake.
//
// Violations of these rules should be reverted in review. The whole point of
// this file is to eliminate the drift-prone class-rewrite pattern that
// testutil.MockCompute / MockDNS / MockBucket used to be.
// ─────────────────────────────────────────────────────────────────────────────

package testutil

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/nacl/box"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	"github.com/getnvoi/nvoi/pkg/provider/github"
	"github.com/getnvoi/nvoi/pkg/provider/hetzner"
)

// Cleanup is the minimal interface NewHetznerFake / NewCloudflareFake need for
// automatic close-on-test-end. *testing.T satisfies it via t.Cleanup(func()).
// Tests that want no auto-close can pass nil and manage Close() themselves.
type Cleanup interface {
	Cleanup(func())
}

// ── OpLog ─────────────────────────────────────────────────────────────────────

// OpLog records every semantic operation a fake performs so tests can assert
// against a flat string list. Shared across all fakes in this file.
type OpLog struct {
	mu      sync.Mutex
	ops     []string
	errOnOp map[string]error
}

// NewOpLog returns an empty, ready-to-use OpLog.
func NewOpLog() *OpLog {
	return &OpLog{errOnOp: map[string]error{}}
}

// Record appends op. If ErrorOn was called for this op, returns that error.
// The error short-circuits the calling HTTP handler to a 500.
func (l *OpLog) Record(op string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, op)
	return l.errOnOp[op]
}

// ErrorOn arranges for Record(op) to return err next time.
func (l *OpLog) ErrorOn(op string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errOnOp[op] = err
}

// Has returns true if op has been recorded at least once.
func (l *OpLog) Has(op string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, o := range l.ops {
		if o == op {
			return true
		}
	}
	return false
}

// Count returns the number of ops with the given prefix.
func (l *OpLog) Count(prefix string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, o := range l.ops {
		if strings.HasPrefix(o, prefix) {
			n++
		}
	}
	return n
}

// IndexOf returns the position of op in the log, or -1 if not found.
func (l *OpLog) IndexOf(op string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, o := range l.ops {
		if o == op {
			return i
		}
	}
	return -1
}

// All returns a copy of every recorded op in order.
func (l *OpLog) All() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]string, len(l.ops))
	copy(cp, l.ops)
	return cp
}

// ── HetznerFake ───────────────────────────────────────────────────────────────

// HetznerFake is a stateful, in-memory Hetzner Cloud API. A real
// hetzner.Client registered via Register() talks to this fake over the wire.
type HetznerFake struct {
	*httptest.Server
	*OpLog

	mu            sync.Mutex
	seqID         int64
	seqAction     int64
	servers       map[int64]*hzServer
	volumes       map[int64]*hzVolume
	firewalls     map[int64]*hzFirewall
	networks      map[int64]*hzNetwork
	actions       map[int64]string // id → "success" | "error"
	actionErrors  map[int64]string
	listServerErr error // when non-nil, GET /servers returns 500
}

type hzServer struct {
	id          int64
	name        string
	status      string
	ipv4        string
	privateIP   string
	diskGB      int
	firewallIDs []int64
	volumeIDs   []int64
	labels      map[string]string
	location    string
}

type hzVolume struct {
	id          int64
	name        string
	size        int
	serverID    int64
	location    string
	linuxDevice string
	labels      map[string]string
}

type hzFirewall struct {
	id     int64
	name   string
	rules  []hzFirewallRule
	labels map[string]string
}

type hzFirewallRule struct {
	Direction string   `json:"direction"`
	Protocol  string   `json:"protocol"`
	Port      string   `json:"port"`
	SourceIPs []string `json:"source_ips"`
}

type hzNetwork struct {
	id     int64
	name   string
	labels map[string]string
}

// NewHetznerFake creates a running fake Hetzner API. Pass *testing.T (or any
// Cleanup) to auto-close at test end. Pass nil to manage f.Close() yourself
// (init-time fakes with process lifetime).
func NewHetznerFake(t Cleanup) *HetznerFake {
	f := &HetznerFake{
		OpLog:        NewOpLog(),
		servers:      map[int64]*hzServer{},
		volumes:      map[int64]*hzVolume{},
		firewalls:    map[int64]*hzFirewall{},
		networks:     map[int64]*hzNetwork{},
		actions:      map[int64]string{},
		actionErrors: map[int64]string{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	if t != nil {
		t.Cleanup(func() { f.Server.Close() })
	}
	return f
}

// Register binds this fake to the infra registry under the given name.
// Real hetzner.Client pointed at f.URL. Overrides any prior registration.
func (f *HetznerFake) Register(name string) {
	schema := provider.CredentialSchema{Name: name}
	provider.RegisterInfra(name, schema, func(creds map[string]string) provider.InfraProvider {
		c := hetzner.New("test-token")
		c.APIClient().BaseURL = f.URL
		return c
	})
}

// FailListServers arms the fake to fail GET /servers with a 500. Used to
// simulate provider API outages in DescribeLive tests.
func (f *HetznerFake) FailListServers(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listServerErr = err
}

// ── HetznerFake: seeders ──

// SeedServer inserts a server into the fake and returns the assigned ID.
func (f *HetznerFake) SeedServer(name, ipv4, privateIP string) *hzServer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addServer(name, ipv4, privateIP, 0)
}

// SeedServerWithDisk inserts a server with a reported diskGB size.
func (f *HetznerFake) SeedServerWithDisk(name, ipv4, privateIP string, diskGB int) *hzServer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addServer(name, ipv4, privateIP, diskGB)
}

func (f *HetznerFake) addServer(name, ipv4, privateIP string, diskGB int) *hzServer {
	f.seqID++
	s := &hzServer{
		id:        f.seqID,
		name:      name,
		status:    "running",
		ipv4:      ipv4,
		privateIP: privateIP,
		diskGB:    diskGB,
	}
	f.servers[s.id] = s
	return s
}

// SeedVolume inserts a volume. serverName="" = unattached.
func (f *HetznerFake) SeedVolume(name string, size int, serverName string) *hzVolume {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seqID++
	v := &hzVolume{
		id:          f.seqID,
		name:        name,
		size:        size,
		linuxDevice: "/dev/sda",
		location:    "fsn1",
	}
	if serverName != "" {
		if srv := f.serverByNameLocked(serverName); srv != nil {
			v.serverID = srv.id
			srv.volumeIDs = append(srv.volumeIDs, v.id)
		}
	}
	f.volumes[v.id] = v
	return v
}

// SeedFirewall inserts a firewall.
func (f *HetznerFake) SeedFirewall(name string) *hzFirewall {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seqID++
	fw := &hzFirewall{id: f.seqID, name: name}
	f.firewalls[fw.id] = fw
	return fw
}

// SeedNetwork inserts a network.
func (f *HetznerFake) SeedNetwork(name string) *hzNetwork {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seqID++
	n := &hzNetwork{id: f.seqID, name: name}
	f.networks[n.id] = n
	return n
}

// Reset clears all state (keeps the server running).
func (f *HetznerFake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.servers = map[int64]*hzServer{}
	f.volumes = map[int64]*hzVolume{}
	f.firewalls = map[int64]*hzFirewall{}
	f.networks = map[int64]*hzNetwork{}
	f.actions = map[int64]string{}
	f.actionErrors = map[int64]string{}
	f.listServerErr = nil
}

// ── HetznerFake: lookups (locked caller) ──

func (f *HetznerFake) serverByNameLocked(name string) *hzServer {
	for _, s := range f.servers {
		if s.name == name {
			return s
		}
	}
	return nil
}

func (f *HetznerFake) volumeByNameLocked(name string) *hzVolume {
	for _, v := range f.volumes {
		if v.name == name {
			return v
		}
	}
	return nil
}

func (f *HetznerFake) firewallByNameLocked(name string) *hzFirewall {
	for _, fw := range f.firewalls {
		if fw.name == name {
			return fw
		}
	}
	return nil
}

func (f *HetznerFake) networkByNameLocked(name string) *hzNetwork {
	for _, n := range f.networks {
		if n.name == name {
			return n
		}
	}
	return nil
}

// ── HetznerFake: HTTP handler ──

var (
	reServersID       = regexp.MustCompile(`^/servers/(\d+)$`)
	reVolumesID       = regexp.MustCompile(`^/volumes/(\d+)$`)
	reVolumesAction   = regexp.MustCompile(`^/volumes/(\d+)/actions/(attach|detach|resize)$`)
	reFirewallsID     = regexp.MustCompile(`^/firewalls/(\d+)$`)
	reFirewallsAction = regexp.MustCompile(`^/firewalls/(\d+)/actions/(set_rules|apply_to_resources|remove_from_resources)$`)
	reNetworksID      = regexp.MustCompile(`^/networks/(\d+)$`)
	reActionsID       = regexp.MustCompile(`^/actions/(\d+)$`)
)

func (f *HetznerFake) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	q := r.URL.Query()

	switch {
	case path == "/datacenters":
		_ = json.NewEncoder(w).Encode(map[string]any{"datacenters": []map[string]any{{}}})
		return
	case path == "/servers" && r.Method == "GET":
		f.handleListServers(w, q)
		return
	case path == "/servers" && r.Method == "POST":
		f.handleCreateServer(w, r)
		return
	case reServersID.MatchString(path) && r.Method == "GET":
		id, _ := strconv.ParseInt(reServersID.FindStringSubmatch(path)[1], 10, 64)
		f.handleGetServer(w, id)
		return
	case reServersID.MatchString(path) && r.Method == "DELETE":
		id, _ := strconv.ParseInt(reServersID.FindStringSubmatch(path)[1], 10, 64)
		f.handleDeleteServer(w, id)
		return

	case path == "/volumes" && r.Method == "GET":
		f.handleListVolumes(w, q)
		return
	case path == "/volumes" && r.Method == "POST":
		f.handleCreateVolume(w, r)
		return
	case reVolumesID.MatchString(path) && r.Method == "GET":
		id, _ := strconv.ParseInt(reVolumesID.FindStringSubmatch(path)[1], 10, 64)
		f.handleGetVolume(w, id)
		return
	case reVolumesID.MatchString(path) && r.Method == "DELETE":
		id, _ := strconv.ParseInt(reVolumesID.FindStringSubmatch(path)[1], 10, 64)
		f.handleDeleteVolume(w, id)
		return
	case reVolumesAction.MatchString(path) && r.Method == "POST":
		m := reVolumesAction.FindStringSubmatch(path)
		id, _ := strconv.ParseInt(m[1], 10, 64)
		f.handleVolumeAction(w, r, id, m[2])
		return

	case path == "/firewalls" && r.Method == "GET":
		f.handleListFirewalls(w, q)
		return
	case path == "/firewalls" && r.Method == "POST":
		f.handleCreateFirewall(w, r)
		return
	case reFirewallsID.MatchString(path) && r.Method == "DELETE":
		id, _ := strconv.ParseInt(reFirewallsID.FindStringSubmatch(path)[1], 10, 64)
		f.handleDeleteFirewall(w, id)
		return
	case reFirewallsAction.MatchString(path) && r.Method == "POST":
		m := reFirewallsAction.FindStringSubmatch(path)
		id, _ := strconv.ParseInt(m[1], 10, 64)
		f.handleFirewallAction(w, r, id, m[2])
		return

	case path == "/networks" && r.Method == "GET":
		f.handleListNetworks(w, q)
		return
	case path == "/networks" && r.Method == "POST":
		f.handleCreateNetwork(w, r)
		return
	case reNetworksID.MatchString(path) && r.Method == "DELETE":
		id, _ := strconv.ParseInt(reNetworksID.FindStringSubmatch(path)[1], 10, 64)
		f.handleDeleteNetwork(w, id)
		return

	case reActionsID.MatchString(path) && r.Method == "GET":
		id, _ := strconv.ParseInt(reActionsID.FindStringSubmatch(path)[1], 10, 64)
		f.handleGetAction(w, id)
		return
	}

	http.Error(w, fmt.Sprintf("hetznerfake: unknown %s %s", r.Method, path), http.StatusNotFound)
}

// ── HetznerFake: handlers ──

func (f *HetznerFake) handleListServers(w http.ResponseWriter, q url.Values) {
	f.mu.Lock()
	if f.listServerErr != nil {
		err := f.listServerErr
		f.mu.Unlock()
		writeAPIError(w, 500, err.Error())
		return
	}
	name := q.Get("name")
	out := make([]map[string]any, 0, len(f.servers))
	for _, s := range f.servers {
		if name != "" && s.name != name {
			continue
		}
		out = append(out, f.serverJSON(s))
	}
	f.mu.Unlock()
	// Record whether the caller filtered by label. pkg/core tests assert that
	// helpers pass app labels (ensuring isolation from foreign servers).
	if selector := q.Get("label_selector"); selector != "" {
		_ = f.Record("list-servers:labeled=" + selector)
	} else if name == "" {
		// Unlabeled list of everything — unsafe in shared accounts.
		_ = f.Record("list-servers:unlabeled")
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"servers": out})
}

func (f *HetznerFake) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string           `json:"name"`
		ServerType string           `json:"server_type"`
		Location   string           `json:"location"`
		Firewalls  []map[string]any `json:"firewalls"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if err := f.Record("ensure-server:" + body.Name); err != nil {
		writeAPIError(w, 500, err.Error())
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.addServer(body.Name, "1.2.3.4", "10.0.1.1", 0)
	s.location = body.Location
	// Attach requested firewalls
	for _, fwref := range body.Firewalls {
		if idVal, ok := fwref["firewall"].(float64); ok {
			s.firewallIDs = append(s.firewallIDs, int64(idVal))
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"server": f.serverJSON(s)})
}

func (f *HetznerFake) handleGetServer(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.servers[id]
	if !ok {
		writeAPIError(w, 404, "server not found")
		return
	}
	obj := f.serverJSON(s)
	_ = json.NewEncoder(w).Encode(map[string]any{"server": obj})
}

func (f *HetznerFake) handleDeleteServer(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	s, ok := f.servers[id]
	if !ok {
		f.mu.Unlock()
		writeAPIError(w, 404, "server not found")
		return
	}
	name := s.name
	delete(f.servers, id)
	// Detach volumes that pointed here
	for vid, v := range f.volumes {
		if v.serverID == id {
			f.volumes[vid].serverID = 0
		}
	}
	f.mu.Unlock()

	if err := f.Record("delete-server:" + name); err != nil {
		// Error injected after the record — re-insert and return 500. We
		// record before actually deleting so tests that assert Has()
		// still pass, but the client sees the error and teardown retries
		// the next step (best-effort).
		writeAPIError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (f *HetznerFake) handleListVolumes(w http.ResponseWriter, q url.Values) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := q.Get("name")
	out := make([]map[string]any, 0, len(f.volumes))
	for _, v := range f.volumes {
		if name != "" && v.name != name {
			continue
		}
		out = append(out, f.volumeJSON(v))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"volumes": out})
}

func (f *HetznerFake) handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Size     int    `json:"size"`
		Location string `json:"location"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := f.Record("ensure-volume:" + body.Name); err != nil {
		writeAPIError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seqID++
	v := &hzVolume{
		id:          f.seqID,
		name:        body.Name,
		size:        body.Size,
		location:    body.Location,
		linuxDevice: "/dev/sda",
	}
	f.volumes[v.id] = v
	_ = json.NewEncoder(w).Encode(map[string]any{"volume": f.volumeJSON(v)})
}

func (f *HetznerFake) handleGetVolume(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.volumes[id]
	if !ok {
		writeAPIError(w, 404, "volume not found")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"volume": f.volumeJSON(v)})
}

func (f *HetznerFake) handleDeleteVolume(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	v, ok := f.volumes[id]
	if !ok {
		f.mu.Unlock()
		writeAPIError(w, 404, "volume not found")
		return
	}
	name := v.name
	delete(f.volumes, id)
	f.mu.Unlock()
	if err := f.Record("delete-volume:" + name); err != nil {
		writeAPIError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (f *HetznerFake) handleVolumeAction(w http.ResponseWriter, r *http.Request, id int64, action string) {
	f.mu.Lock()
	v, ok := f.volumes[id]
	if !ok {
		f.mu.Unlock()
		writeAPIError(w, 404, "volume not found")
		return
	}
	name := v.name
	switch action {
	case "detach":
		v.serverID = 0
	case "attach":
		var body struct {
			Server int64 `json:"server"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		v.serverID = body.Server
	case "resize":
		var body struct {
			Size int `json:"size"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		v.size = body.Size
	}
	f.mu.Unlock()
	if action == "detach" {
		if err := f.Record("detach-volume:" + name); err != nil {
			writeAPIError(w, 500, err.Error())
			return
		}
	}
	actionID := f.completeAction()
	_ = json.NewEncoder(w).Encode(map[string]any{"action": map[string]any{"id": actionID, "status": "success"}})
}

func (f *HetznerFake) handleListFirewalls(w http.ResponseWriter, q url.Values) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := q.Get("name")
	out := make([]map[string]any, 0, len(f.firewalls))
	for _, fw := range f.firewalls {
		if name != "" && fw.name != name {
			continue
		}
		out = append(out, f.firewallJSON(fw))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"firewalls": out})
}

func (f *HetznerFake) handleCreateFirewall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string           `json:"name"`
		Rules []hzFirewallRule `json:"rules"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.seqID++
	fw := &hzFirewall{id: f.seqID, name: body.Name, rules: body.Rules}
	f.firewalls[fw.id] = fw
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"firewall": map[string]any{"id": fw.id, "name": fw.name}})
}

func (f *HetznerFake) handleDeleteFirewall(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	fw, ok := f.firewalls[id]
	if !ok {
		f.mu.Unlock()
		writeAPIError(w, 404, "firewall not found")
		return
	}
	name := fw.name
	delete(f.firewalls, id)
	f.mu.Unlock()
	if err := f.Record("delete-firewall:" + name); err != nil {
		writeAPIError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (f *HetznerFake) handleFirewallAction(w http.ResponseWriter, r *http.Request, id int64, action string) {
	f.mu.Lock()
	fw, ok := f.firewalls[id]
	if !ok {
		f.mu.Unlock()
		writeAPIError(w, 404, "firewall not found")
		return
	}
	name := fw.name
	switch action {
	case "set_rules":
		var body struct {
			Rules []hzFirewallRule `json:"rules"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fw.rules = body.Rules
	case "apply_to_resources":
		// attach firewall to server(s) — track for clean test behavior
		var body struct {
			ApplyTo []struct {
				Type   string `json:"type"`
				Server struct {
					ID int64 `json:"id"`
				} `json:"server"`
			} `json:"apply_to"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, a := range body.ApplyTo {
			if s, ok := f.servers[a.Server.ID]; ok {
				s.firewallIDs = append(s.firewallIDs, id)
			}
		}
	case "remove_from_resources":
		var body struct {
			RemoveFrom []struct {
				Type   string `json:"type"`
				Server struct {
					ID int64 `json:"id"`
				} `json:"server"`
			} `json:"remove_from"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, a := range body.RemoveFrom {
			if s, ok := f.servers[a.Server.ID]; ok {
				out := s.firewallIDs[:0]
				for _, fid := range s.firewallIDs {
					if fid != id {
						out = append(out, fid)
					}
				}
				s.firewallIDs = out
			}
		}
	}
	f.mu.Unlock()

	switch action {
	case "set_rules":
		if err := f.Record("firewall:" + name); err != nil {
			writeAPIError(w, 500, err.Error())
			return
		}
	case "remove_from_resources":
		_ = f.Record("detach-firewall:" + name)
	case "apply_to_resources":
		_ = f.Record("attach-firewall:" + name)
	}

	switch action {
	case "set_rules":
		// set_rules returns the updated firewall, no action object
		_ = json.NewEncoder(w).Encode(map[string]any{"firewall": map[string]any{"id": id}})
	default:
		// action-based endpoints return a list of actions
		actionID := f.completeAction()
		_ = json.NewEncoder(w).Encode(map[string]any{"actions": []map[string]any{{"id": actionID, "status": "success"}}})
	}
}

func (f *HetznerFake) handleListNetworks(w http.ResponseWriter, q url.Values) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := q.Get("name")
	out := make([]map[string]any, 0, len(f.networks))
	for _, n := range f.networks {
		if name != "" && n.name != name {
			continue
		}
		out = append(out, map[string]any{"id": n.id, "name": n.name})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"networks": out})
}

func (f *HetznerFake) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.seqID++
	n := &hzNetwork{id: f.seqID, name: body.Name}
	f.networks[n.id] = n
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"network": map[string]any{"id": n.id, "name": n.name}})
}

func (f *HetznerFake) handleDeleteNetwork(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	n, ok := f.networks[id]
	if !ok {
		f.mu.Unlock()
		writeAPIError(w, 404, "network not found")
		return
	}
	name := n.name
	delete(f.networks, id)
	f.mu.Unlock()
	if err := f.Record("delete-network:" + name); err != nil {
		writeAPIError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (f *HetznerFake) handleGetAction(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	status, ok := f.actions[id]
	errMsg := f.actionErrors[id]
	f.mu.Unlock()
	if !ok {
		status = "success"
	}
	obj := map[string]any{"id": id, "status": status}
	if status == "error" {
		obj["error"] = map[string]any{"message": errMsg}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"action": obj})
}

func (f *HetznerFake) completeAction() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seqAction++
	id := f.seqAction
	f.actions[id] = "success"
	return id
}

// ── HetznerFake: JSON encoders ──

func (f *HetznerFake) serverJSON(s *hzServer) map[string]any {
	fws := make([]map[string]any, 0, len(s.firewallIDs))
	for _, fid := range s.firewallIDs {
		fws = append(fws, map[string]any{"id": fid})
	}
	priv := []map[string]string{}
	if s.privateIP != "" {
		priv = append(priv, map[string]string{"ip": s.privateIP})
	}
	return map[string]any{
		"id":     s.id,
		"name":   s.name,
		"status": s.status,
		"public_net": map[string]any{
			"ipv4":      map[string]string{"ip": s.ipv4},
			"ipv6":      map[string]string{"ip": ""},
			"firewalls": fws,
		},
		"private_net": priv,
		"server_type": map[string]any{"disk": s.diskGB},
		"volumes":     s.volumeIDs,
		"datacenter":  map[string]any{"location": map[string]string{"name": s.location}},
	}
}

func (f *HetznerFake) volumeJSON(v *hzVolume) map[string]any {
	obj := map[string]any{
		"id":           v.id,
		"name":         v.name,
		"size":         v.size,
		"location":     map[string]string{"name": v.location},
		"linux_device": v.linuxDevice,
		"status":       "available",
		"labels":       map[string]string{},
	}
	if v.serverID != 0 {
		obj["server"] = v.serverID
	} else {
		obj["server"] = nil
	}
	return obj
}

func (f *HetznerFake) firewallJSON(fw *hzFirewall) map[string]any {
	return map[string]any{
		"id":    fw.id,
		"name":  fw.name,
		"rules": fw.rules,
	}
}

// ── CloudflareFake ────────────────────────────────────────────────────────────

// CloudflareFake is a stateful, in-memory Cloudflare API covering BOTH the
// DNS (/zones/…) and R2 (/accounts/…/r2/…) surfaces, plus the R2 S3-compatible
// ops that cloudflare.Client makes directly. One httptest.Server handles all
// of it.
type CloudflareFake struct {
	*httptest.Server
	*OpLog

	mu         sync.Mutex
	zoneID     string
	zoneDomain string
	accountID  string
	tokenID    string
	recSeq     int
	records    map[string]*cfDNSRec // id → record
	buckets    map[string]*cfBucket // name → bucket
}

type cfDNSRec struct {
	ID      string
	Type    string
	Name    string // FQDN
	Content string
	Proxied bool
	TTL     int
}

type cfBucket struct {
	Name    string
	Objects map[string][]byte // key → body
	// CORS / lifecycle omitted — we only record the ops.
}

// CloudflareFakeOptions configures a CloudflareFake. Defaults work for most
// tests.
type CloudflareFakeOptions struct {
	ZoneID     string // default "Z1"
	ZoneDomain string // default "myapp.com"
	AccountID  string // default "testacct"
	TokenID    string // default "test-token-id"
}

// NewCloudflareFake returns a running CF fake. Pass nil for Cleanup to manage
// lifetime manually.
func NewCloudflareFake(t Cleanup, opts CloudflareFakeOptions) *CloudflareFake {
	if opts.ZoneID == "" {
		opts.ZoneID = "Z1"
	}
	if opts.ZoneDomain == "" {
		opts.ZoneDomain = "myapp.com"
	}
	if opts.AccountID == "" {
		opts.AccountID = "testacct"
	}
	if opts.TokenID == "" {
		opts.TokenID = "test-token-id"
	}
	f := &CloudflareFake{
		OpLog:      NewOpLog(),
		zoneID:     opts.ZoneID,
		zoneDomain: opts.ZoneDomain,
		accountID:  opts.AccountID,
		tokenID:    opts.TokenID,
		records:    map[string]*cfDNSRec{},
		buckets:    map[string]*cfBucket{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	if t != nil {
		t.Cleanup(func() { f.Server.Close() })
	}
	return f
}

// RegisterDNS binds this fake as a DNS provider.
func (f *CloudflareFake) RegisterDNS(name string) {
	schema := provider.CredentialSchema{Name: name}
	provider.RegisterDNS(name, schema, func(creds map[string]string) provider.DNSProvider {
		effective := map[string]string{
			"api_key": "test",
			"zone_id": f.zoneID,
			"zone":    f.zoneDomain,
		}
		for k, v := range creds {
			effective[k] = v
		}
		c := cloudflare.NewDNS(effective)
		c.APIClient().BaseURL = f.URL
		return c
	})
}

// RegisterBucket binds this fake as a bucket provider (Cloudflare R2).
func (f *CloudflareFake) RegisterBucket(name string) {
	schema := provider.CredentialSchema{Name: name}
	provider.RegisterBucket(name, schema, func(creds map[string]string) provider.BucketProvider {
		effective := map[string]string{
			"api_key":    "test",
			"account_id": f.accountID,
		}
		for k, v := range creds {
			effective[k] = v
		}
		c := cloudflare.NewBucket(effective)
		c.APIClient().BaseURL = f.URL
		c.SetS3EndpointOverride(f.URL)
		return c
	})
}

// ── CloudflareFake: seeders ──

// SeedDNSRecord inserts a DNS record. rtype should be "A" or "AAAA".
func (f *CloudflareFake) SeedDNSRecord(fqdn, ip, rtype string) *cfDNSRec {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recSeq++
	rec := &cfDNSRec{
		ID:      fmt.Sprintf("rec-%d", f.recSeq),
		Type:    rtype,
		Name:    fqdn,
		Content: ip,
	}
	f.records[rec.ID] = rec
	return rec
}

// SeedBucket creates an empty bucket in the fake.
func (f *CloudflareFake) SeedBucket(name string) *cfBucket {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := &cfBucket{Name: name, Objects: map[string][]byte{}}
	f.buckets[name] = b
	return b
}

// SeedBucketObject adds an object to an existing bucket.
func (f *CloudflareFake) SeedBucketObject(bucket, key string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.buckets[bucket]
	if !ok {
		b = &cfBucket{Name: bucket, Objects: map[string][]byte{}}
		f.buckets[bucket] = b
	}
	b.Objects[key] = body
}

// ── CloudflareFake: HTTP handler ──

var (
	reZoneRecords    = regexp.MustCompile(`^/zones/([^/]+)/dns_records$`)
	reZoneRecordsID  = regexp.MustCompile(`^/zones/([^/]+)/dns_records/([^/]+)$`)
	reAcctBuckets    = regexp.MustCompile(`^/accounts/([^/]+)/r2/buckets$`)
	reAcctBucketName = regexp.MustCompile(`^/accounts/([^/]+)/r2/buckets/([^/]+)$`)
)

func (f *CloudflareFake) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Token verify
	if path == "/user/tokens/verify" && r.Method == "GET" {
		f.mu.Lock()
		id := f.tokenID
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result":  map[string]any{"id": id, "status": "active"},
			"success": true,
		})
		return
	}

	// DNS records
	if m := reZoneRecords.FindStringSubmatch(path); m != nil {
		switch r.Method {
		case "GET":
			f.handleDNSList(w, r, m[1])
		case "POST":
			f.handleDNSCreate(w, r, m[1])
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	if m := reZoneRecordsID.FindStringSubmatch(path); m != nil {
		switch r.Method {
		case "DELETE":
			f.handleDNSDelete(w, m[2])
		case "PUT":
			f.handleDNSUpdate(w, r, m[2])
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}

	// R2 REST API
	if m := reAcctBuckets.FindStringSubmatch(path); m != nil {
		switch r.Method {
		case "POST":
			f.handleR2Create(w, r, m[1])
		case "GET":
			f.handleR2List(w, m[1])
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	if m := reAcctBucketName.FindStringSubmatch(path); m != nil {
		switch r.Method {
		case "DELETE":
			f.handleR2Delete(w, m[2])
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}

	// S3-compatible ops use path = /<bucket> or /<bucket>?<op>
	if r.Method == "GET" && strings.HasPrefix(path, "/") && len(path) > 1 && !strings.Contains(path[1:], "/") {
		bucket := strings.TrimPrefix(path, "/")
		f.handleS3List(w, r, bucket)
		return
	}
	if r.Method == "POST" && strings.HasPrefix(path, "/") && r.URL.Query().Has("delete") {
		bucket := strings.TrimPrefix(path, "/")
		f.handleS3Delete(w, r, bucket)
		return
	}
	if r.Method == "PUT" && r.URL.Query().Has("cors") {
		bucket := strings.TrimPrefix(path, "/")
		f.handleS3SetCORS(w, bucket)
		return
	}
	if r.Method == "DELETE" && r.URL.Query().Has("cors") {
		bucket := strings.TrimPrefix(path, "/")
		f.handleS3ClearCORS(w, bucket)
		return
	}
	if r.Method == "PUT" && r.URL.Query().Has("lifecycle") {
		bucket := strings.TrimPrefix(path, "/")
		f.handleS3SetLifecycle(w, bucket)
		return
	}

	http.Error(w, fmt.Sprintf("cloudflarefake: unknown %s %s", r.Method, r.URL.String()), http.StatusNotFound)
}

// ── CloudflareFake: DNS handlers ──

func (f *CloudflareFake) handleDNSList(w http.ResponseWriter, r *http.Request, zoneID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	name := q.Get("name")
	rtype := q.Get("type")
	out := make([]map[string]any, 0, len(f.records))
	for _, rec := range f.records {
		if name != "" && rec.Name != name {
			continue
		}
		if rtype != "" && rec.Type != rtype {
			continue
		}
		out = append(out, map[string]any{
			"id":      rec.ID,
			"type":    rec.Type,
			"name":    rec.Name,
			"content": rec.Content,
			"proxied": rec.Proxied,
			"ttl":     rec.TTL,
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": out, "success": true})
}

func (f *CloudflareFake) handleDNSCreate(w http.ResponseWriter, r *http.Request, zoneID string) {
	var body struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
		TTL     int    `json:"ttl"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := f.Record("ensure-dns:" + body.Name); err != nil {
		writeCFError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	f.recSeq++
	rec := &cfDNSRec{
		ID:      fmt.Sprintf("rec-%d", f.recSeq),
		Type:    body.Type,
		Name:    body.Name,
		Content: body.Content,
		Proxied: body.Proxied,
		TTL:     body.TTL,
	}
	f.records[rec.ID] = rec
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"id": rec.ID}, "success": true})
}

func (f *CloudflareFake) handleDNSUpdate(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	rec, ok := f.records[id]
	if ok {
		rec.Type = body.Type
		rec.Name = body.Name
		rec.Content = body.Content
		rec.Proxied = body.Proxied
	}
	f.mu.Unlock()
	if !ok {
		writeCFError(w, 404, "record not found")
		return
	}
	_ = f.Record("ensure-dns:" + body.Name)
	_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"id": id}, "success": true})
}

func (f *CloudflareFake) handleDNSDelete(w http.ResponseWriter, id string) {
	f.mu.Lock()
	rec, ok := f.records[id]
	if !ok {
		f.mu.Unlock()
		writeCFError(w, 404, "record not found")
		return
	}
	name := rec.Name
	delete(f.records, id)
	f.mu.Unlock()
	if err := f.Record("delete-dns:" + name); err != nil {
		writeCFError(w, 500, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"id": id}, "success": true})
}

// ── CloudflareFake: R2 REST handlers ──

func (f *CloudflareFake) handleR2Create(w http.ResponseWriter, r *http.Request, acct string) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := f.Record("ensure-bucket:" + body.Name); err != nil {
		writeCFError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	if _, exists := f.buckets[body.Name]; exists {
		f.mu.Unlock()
		writeCFError(w, 409, "bucket already exists")
		return
	}
	f.buckets[body.Name] = &cfBucket{Name: body.Name, Objects: map[string][]byte{}}
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"name": body.Name}, "success": true})
}

func (f *CloudflareFake) handleR2List(w http.ResponseWriter, acct string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	buckets := make([]map[string]any, 0, len(f.buckets))
	for name := range f.buckets {
		buckets = append(buckets, map[string]any{"name": name})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"buckets": buckets}, "success": true})
}

func (f *CloudflareFake) handleR2Delete(w http.ResponseWriter, name string) {
	f.mu.Lock()
	_, ok := f.buckets[name]
	if !ok {
		f.mu.Unlock()
		writeCFError(w, 404, "bucket not found")
		return
	}
	delete(f.buckets, name)
	f.mu.Unlock()
	if err := f.Record("delete-bucket:" + name); err != nil {
		writeCFError(w, 500, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": nil, "success": true})
}

// ── CloudflareFake: S3 handlers ──

func (f *CloudflareFake) handleS3List(w http.ResponseWriter, r *http.Request, bucket string) {
	// Record "empty-bucket:X" on the initial list — EmptyBucket always starts
	// with GET ?list-type=2, even when the bucket is empty.
	if err := f.Record("empty-bucket:" + bucket); err != nil {
		writeS3Error(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.buckets[bucket]
	if !ok {
		w.WriteHeader(404)
		return
	}
	type listObj struct {
		Key          string `xml:"Key"`
		Size         int    `xml:"Size"`
		LastModified string `xml:"LastModified"`
	}
	type listResp struct {
		XMLName     xml.Name  `xml:"ListBucketResult"`
		Contents    []listObj `xml:"Contents"`
		IsTruncated bool      `xml:"IsTruncated"`
	}
	out := listResp{}
	for k := range b.Objects {
		out.Contents = append(out.Contents, listObj{Key: k, Size: len(b.Objects[k]), LastModified: "2024-01-01T00:00:00Z"})
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(out)
}

func (f *CloudflareFake) handleS3Delete(w http.ResponseWriter, r *http.Request, bucket string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.buckets[bucket]
	if !ok {
		w.WriteHeader(404)
		return
	}
	// Parse list of object keys
	body, _ := io.ReadAll(r.Body)
	type del struct {
		XMLName xml.Name `xml:"Delete"`
		Objects []struct {
			Key string `xml:"Key"`
		} `xml:"Object"`
	}
	var parsed del
	_ = xml.Unmarshal(body, &parsed)
	for _, obj := range parsed.Objects {
		delete(b.Objects, obj.Key)
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(`<DeleteResult/>`))
}

func (f *CloudflareFake) handleS3SetCORS(w http.ResponseWriter, bucket string) {
	_ = f.Record("set-cors:" + bucket)
	w.WriteHeader(200)
}

func (f *CloudflareFake) handleS3ClearCORS(w http.ResponseWriter, bucket string) {
	_ = f.Record("clear-cors:" + bucket)
	w.WriteHeader(204)
}

func (f *CloudflareFake) handleS3SetLifecycle(w http.ResponseWriter, bucket string) {
	_ = f.Record("set-lifecycle:" + bucket)
	w.WriteHeader(200)
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": "error", "message": msg},
	})
}

func writeCFError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"errors":  []map[string]any{{"code": status, "message": msg}},
	})
}

func writeS3Error(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = fmt.Fprintf(w, `<Error><Message>%s</Message></Error>`, msg)
}

// ── GitHubFake ────────────────────────────────────────────────────────────────

// GitHubFake is a stateful, in-memory GitHub REST API covering every endpoint
// the CIProvider for `github` touches: /user, /repos/{o}/{r}, Actions secrets
// (public-key + PUT + list), Contents, git refs, pulls, rulesets, and classic
// branch protection.
//
// The fake generates a real curve25519 keypair at construction — the public
// half is exposed via /actions/secrets/public-key, the private half stays
// internal. Sealed secrets that tests PUT are decrypted on the spot, so the
// fake holds plaintext values that tests assert against (sealed-box
// correctness is verified end-to-end without reimplementing sealing).
//
// State model is repo-scoped via SeedRepo — the fake refuses any request to
// an unseeded (owner, repo) pair. SeedRuleset marks a branch as protected;
// SeedProtection simulates a classic branch-protection object. A protected
// default branch makes the Contents PUT return 422 with the standard
// "protected branch" body, triggering GitHubCI's PR fallback path.
type GitHubFake struct {
	*httptest.Server
	*OpLog

	mu         sync.Mutex
	userLogin  string
	publicKey  [32]byte
	privateKey [32]byte
	keyID      string

	repos map[string]*ghRepo // "owner/repo" → repo
}

type ghRepo struct {
	Owner         string
	Name          string
	DefaultBranch string

	// Secrets: name → plaintext (decrypted on PUT).
	Secrets map[string]string

	// Rulesets: branch → non-empty rule list ⇒ protected.
	Rulesets map[string][]string

	// Classic protection: branch → protected bool.
	Protection map[string]bool

	// Contents: (branch, path) → content bytes. Includes commit SHAs
	// synthesized per write so the Contents API's SHA-update flow works.
	Contents map[string]*ghFile

	// Branches: name → head SHA.
	Branches map[string]string

	// PRs opened via POST /pulls. head → URL.
	PRs map[string]string

	// Monotonic SHA source so every PUT produces a fresh SHA.
	shaSeq int
}

type ghFile struct {
	SHA     string
	Content []byte
}

// NewGitHubFake returns a running GitHub API fake. Pass nil for Cleanup to
// manage lifetime manually.
func NewGitHubFake(t Cleanup) *GitHubFake {
	f := &GitHubFake{
		OpLog:     NewOpLog(),
		userLogin: "testuser",
		keyID:     "key-1",
		repos:     map[string]*ghRepo{},
	}
	// Generate a real sealed-box keypair so the fake can decrypt secrets
	// that the provider seals. Fixed-length [32]byte — no fallback.
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		panic(fmt.Sprintf("GitHubFake: generate keypair: %v", err))
	}
	f.publicKey = *pub
	f.privateKey = *priv
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	if t != nil {
		t.Cleanup(func() { f.Server.Close() })
	}
	return f
}

// Register binds this fake as a CI provider named `name`. The real
// github.GitHubCI is constructed and its BaseURL is repointed at the fake.
func (f *GitHubFake) Register(name string) {
	schema := provider.CredentialSchema{
		Name: name,
		Fields: []provider.CredentialField{
			{Key: "token", Required: true, EnvVar: "GITHUB_TOKEN", Flag: "github-token"},
			{Key: "repo", Required: false, EnvVar: "GITHUB_REPO", Flag: "github-repo"},
		},
	}
	provider.RegisterCI(name, schema, func(creds map[string]string) provider.CIProvider {
		c := github.New(creds["token"], creds["repo"])
		c.SetBaseURL(f.URL)
		return c
	})
}

// ── GitHubFake: seeders ──

// SeedRepo inserts a repo the fake knows about. Required before any other
// API call against (owner, repo) succeeds — reflects how GitHub itself
// behaves (unknown repo ⇒ 404).
func (f *GitHubFake) SeedRepo(owner, repo, defaultBranch string) *ghRepo {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := owner + "/" + repo
	r := &ghRepo{
		Owner:         owner,
		Name:          repo,
		DefaultBranch: defaultBranch,
		Secrets:       map[string]string{},
		Rulesets:      map[string][]string{},
		Protection:    map[string]bool{},
		Contents:      map[string]*ghFile{},
		Branches:      map[string]string{defaultBranch: "sha-base-0"},
		PRs:           map[string]string{},
	}
	f.repos[key] = r
	return r
}

// SeedRuleset marks `branch` as covered by a repository ruleset. The
// ruleset list contents don't matter for our checks — only "non-empty"
// does — so rules is a free-form label list.
func (f *GitHubFake) SeedRuleset(owner, repo, branch string, rules ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.repos[owner+"/"+repo]
	if !ok {
		panic(fmt.Sprintf("SeedRuleset: repo %s/%s not seeded", owner, repo))
	}
	r.Rulesets[branch] = append([]string{}, rules...)
}

// SeedProtection marks `branch` as covered by classic branch protection.
// Independent of rulesets — both gates produce protected behavior.
func (f *GitHubFake) SeedProtection(owner, repo, branch string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.repos[owner+"/"+repo]
	if !ok {
		panic(fmt.Sprintf("SeedProtection: repo %s/%s not seeded", owner, repo))
	}
	r.Protection[branch] = true
}

// SecretValue returns the decrypted plaintext of a secret previously
// synced via SyncSecrets. Tests use this to assert the provider sealed
// the right value against the right key.
func (f *GitHubFake) SecretValue(owner, repo, name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.repos[owner+"/"+repo]
	if !ok {
		return "", false
	}
	v, ok := r.Secrets[name]
	return v, ok
}

// FileContent returns the bytes of a file previously committed via the
// Contents API, from the named branch.
func (f *GitHubFake) FileContent(owner, repo, branch, path string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.repos[owner+"/"+repo]
	if !ok {
		return nil, false
	}
	g, ok := r.Contents[branch+":"+path]
	if !ok {
		return nil, false
	}
	return append([]byte{}, g.Content...), true
}

// ── GitHubFake: HTTP handler ──

// ghRepoPathRE extracts owner and repo from /repos/{owner}/{repo}/... paths.
var ghRepoPathRE = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)(/.*)?$`)

func (f *GitHubFake) serve(w http.ResponseWriter, r *http.Request) {
	// /user — token probe.
	if r.URL.Path == "/user" && r.Method == "GET" {
		if err := f.Record("github:get-user"); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"login": f.userLogin})
		return
	}

	m := ghRepoPathRE.FindStringSubmatch(r.URL.Path)
	if m == nil {
		writeGHError(w, 404, "not found: "+r.URL.Path)
		return
	}
	owner, repo, rest := m[1], m[2], m[3]

	f.mu.Lock()
	repoState, ok := f.repos[owner+"/"+repo]
	f.mu.Unlock()
	if !ok {
		writeGHError(w, 404, "unknown repo "+owner+"/"+repo)
		return
	}

	// Dispatch on sub-path.
	switch {
	case rest == "" || rest == "/":
		f.handleRepoRoot(w, r, repoState)
	case rest == "/actions/secrets/public-key":
		f.handlePublicKey(w, r)
	case rest == "/actions/secrets" || strings.HasPrefix(rest, "/actions/secrets?"):
		f.handleListSecrets(w, r, repoState)
	case strings.HasPrefix(rest, "/actions/secrets/"):
		name := strings.TrimPrefix(rest, "/actions/secrets/")
		f.handlePutSecret(w, r, repoState, name)
	case strings.HasPrefix(rest, "/rules/branches/"):
		branch := strings.TrimPrefix(rest, "/rules/branches/")
		f.handleRulesets(w, r, repoState, branch)
	case strings.HasPrefix(rest, "/branches/") && strings.HasSuffix(rest, "/protection"):
		branch := strings.TrimSuffix(strings.TrimPrefix(rest, "/branches/"), "/protection")
		f.handleProtection(w, r, repoState, branch)
	case strings.HasPrefix(rest, "/contents/"):
		path := strings.TrimPrefix(rest, "/contents/")
		f.handleContents(w, r, repoState, path)
	case strings.HasPrefix(rest, "/git/ref/heads/"):
		branch := strings.TrimPrefix(rest, "/git/ref/heads/")
		f.handleGetRef(w, r, repoState, branch)
	case rest == "/git/refs":
		f.handleCreateRef(w, r, repoState)
	case rest == "/pulls" || strings.HasPrefix(rest, "/pulls?"):
		f.handlePulls(w, r, repoState)
	default:
		writeGHError(w, 404, "unmapped path "+rest)
	}
}

func (f *GitHubFake) handleRepoRoot(w http.ResponseWriter, r *http.Request, repo *ghRepo) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:get-repo:%s/%s", repo.Owner, repo.Name)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"full_name":      repo.Owner + "/" + repo.Name,
		"default_branch": repo.DefaultBranch,
	})
}

func (f *GitHubFake) handlePublicKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record("github:get-public-key"); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"key":    base64.StdEncoding.EncodeToString(f.publicKey[:]),
		"key_id": f.keyID,
	})
}

func (f *GitHubFake) handleListSecrets(w http.ResponseWriter, r *http.Request, repo *ghRepo) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:list-secrets:%s/%s", repo.Owner, repo.Name)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	secrets := make([]map[string]string, 0, len(repo.Secrets))
	for name := range repo.Secrets {
		secrets = append(secrets, map[string]string{"name": name})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"total_count": len(secrets),
		"secrets":     secrets,
	})
}

func (f *GitHubFake) handlePutSecret(w http.ResponseWriter, r *http.Request, repo *ghRepo, name string) {
	if r.Method != "PUT" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:put-secret:%s/%s:%s", repo.Owner, repo.Name, name)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	var body struct {
		EncryptedValue string `json:"encrypted_value"`
		KeyID          string `json:"key_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeGHError(w, 400, "decode body: "+err.Error())
		return
	}
	if body.KeyID != f.keyID {
		writeGHError(w, 422, "wrong key_id")
		return
	}
	ciphertext, err := base64.StdEncoding.DecodeString(body.EncryptedValue)
	if err != nil {
		writeGHError(w, 422, "bad base64")
		return
	}
	plaintext, ok := box.OpenAnonymous(nil, ciphertext, &f.publicKey, &f.privateKey)
	if !ok {
		writeGHError(w, 422, "sealed-box decrypt failed")
		return
	}
	f.mu.Lock()
	repo.Secrets[name] = string(plaintext)
	f.mu.Unlock()
	w.WriteHeader(201)
}

func (f *GitHubFake) handleRulesets(w http.ResponseWriter, r *http.Request, repo *ghRepo, branch string) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:get-rulesets:%s/%s:%s", repo.Owner, repo.Name, branch)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	rules := repo.Rulesets[branch]
	f.mu.Unlock()
	out := make([]map[string]string, len(rules))
	for i, r := range rules {
		out[i] = map[string]string{"type": r}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *GitHubFake) handleProtection(w http.ResponseWriter, r *http.Request, repo *ghRepo, branch string) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:get-protection:%s/%s:%s", repo.Owner, repo.Name, branch)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	protected := repo.Protection[branch]
	f.mu.Unlock()
	if !protected {
		writeGHError(w, 404, "not protected")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"url": "protection-url"})
}

func (f *GitHubFake) handleContents(w http.ResponseWriter, r *http.Request, repo *ghRepo, path string) {
	branch := r.URL.Query().Get("ref")
	switch r.Method {
	case "GET":
		if branch == "" {
			branch = repo.DefaultBranch
		}
		if err := f.Record(fmt.Sprintf("github:get-contents:%s/%s:%s:%s", repo.Owner, repo.Name, branch, path)); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		f.mu.Lock()
		g, ok := repo.Contents[branch+":"+path]
		f.mu.Unlock()
		if !ok {
			writeGHError(w, 404, "no file")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": g.SHA, "path": path})
	case "PUT":
		var body struct {
			Message string `json:"message"`
			Content string `json:"content"`
			Branch  string `json:"branch"`
			SHA     string `json:"sha"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeGHError(w, 400, "decode body: "+err.Error())
			return
		}
		if body.Branch == "" {
			body.Branch = repo.DefaultBranch
		}
		if err := f.Record(fmt.Sprintf("github:put-contents:%s/%s:%s:%s", repo.Owner, repo.Name, body.Branch, path)); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		// Reject direct push on protected default branch.
		f.mu.Lock()
		isDefault := body.Branch == repo.DefaultBranch
		isRuleset := len(repo.Rulesets[body.Branch]) > 0
		isClassic := repo.Protection[body.Branch]
		f.mu.Unlock()
		if isDefault && (isRuleset || isClassic) {
			writeGHError(w, 422, "repository rule violations: protected branch disallows direct push")
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(body.Content)
		if err != nil {
			writeGHError(w, 422, "bad base64 content")
			return
		}
		f.mu.Lock()
		repo.shaSeq++
		sha := fmt.Sprintf("sha-%d", repo.shaSeq)
		repo.Contents[body.Branch+":"+path] = &ghFile{SHA: sha, Content: decoded}
		repo.Branches[body.Branch] = sha
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"sha": sha, "path": path}})
	default:
		writeGHError(w, 405, "method not allowed")
	}
}

func (f *GitHubFake) handleGetRef(w http.ResponseWriter, r *http.Request, repo *ghRepo, branch string) {
	if r.Method != "GET" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	if err := f.Record(fmt.Sprintf("github:get-ref:%s/%s:%s", repo.Owner, repo.Name, branch)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	sha, ok := repo.Branches[branch]
	f.mu.Unlock()
	if !ok {
		writeGHError(w, 404, "no ref")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ref":    "refs/heads/" + branch,
		"object": map[string]string{"sha": sha},
	})
}

func (f *GitHubFake) handleCreateRef(w http.ResponseWriter, r *http.Request, repo *ghRepo) {
	if r.Method != "POST" {
		writeGHError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeGHError(w, 400, "decode body: "+err.Error())
		return
	}
	branch := strings.TrimPrefix(body.Ref, "refs/heads/")
	if err := f.Record(fmt.Sprintf("github:create-ref:%s/%s:%s", repo.Owner, repo.Name, branch)); err != nil {
		writeGHError(w, 500, err.Error())
		return
	}
	f.mu.Lock()
	if _, exists := repo.Branches[branch]; exists {
		f.mu.Unlock()
		writeGHError(w, 422, "ref already exists")
		return
	}
	repo.Branches[branch] = body.SHA
	f.mu.Unlock()
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(map[string]any{"ref": body.Ref, "object": map[string]string{"sha": body.SHA}})
}

func (f *GitHubFake) handlePulls(w http.ResponseWriter, r *http.Request, repo *ghRepo) {
	switch r.Method {
	case "GET":
		headQ := r.URL.Query().Get("head") // "owner:branch"
		if err := f.Record(fmt.Sprintf("github:list-pulls:%s/%s:%s", repo.Owner, repo.Name, headQ)); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		// Strip "owner:" prefix to get the branch name for lookup.
		branch := headQ
		if idx := strings.Index(headQ, ":"); idx >= 0 {
			branch = headQ[idx+1:]
		}
		f.mu.Lock()
		url, ok := repo.PRs[branch]
		f.mu.Unlock()
		if !ok {
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{{"html_url": url}})
	case "POST":
		var body struct {
			Title string `json:"title"`
			Head  string `json:"head"`
			Base  string `json:"base"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeGHError(w, 400, "decode body: "+err.Error())
			return
		}
		if err := f.Record(fmt.Sprintf("github:create-pull:%s/%s:%s->%s", repo.Owner, repo.Name, body.Head, body.Base)); err != nil {
			writeGHError(w, 500, err.Error())
			return
		}
		f.mu.Lock()
		if _, exists := repo.PRs[body.Head]; exists {
			f.mu.Unlock()
			writeGHError(w, 422, "PR already exists")
			return
		}
		url := fmt.Sprintf("https://github.com/%s/%s/pull/1", repo.Owner, repo.Name)
		repo.PRs[body.Head] = url
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"html_url": url, "number": 1})
	default:
		writeGHError(w, 405, "method not allowed")
	}
}

// writeGHError emits an error in the shape GitHub's REST API uses: a JSON
// body with a top-level "message" field. utils.APIError unmarshals the
// body raw so status code drives the IsNotFound / isProtectedBranchError
// checks; the message is for logs.
func writeGHError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": msg})
}
