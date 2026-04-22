package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"sync"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/hetzner"
)

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
	registerInfra(name, func(creds map[string]string) provider.InfraProvider {
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
	f.mu.Unlock()

	if err := f.Record("delete-server:" + name); err != nil {
		writeAPIError(w, 500, err.Error())
		return
	}

	f.mu.Lock()
	delete(f.servers, id)
	// Detach volumes that pointed here
	for vid, v := range f.volumes {
		if v.serverID == id {
			f.volumes[vid].serverID = 0
		}
	}
	f.mu.Unlock()

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
	f.mu.Unlock()

	if err := f.Record("delete-volume:" + name); err != nil {
		writeAPIError(w, 500, err.Error())
		return
	}

	f.mu.Lock()
	delete(f.volumes, id)
	f.mu.Unlock()

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
	f.mu.Unlock()

	if err := f.Record("delete-firewall:" + name); err != nil {
		writeAPIError(w, 500, err.Error())
		return
	}

	f.mu.Lock()
	delete(f.firewalls, id)
	f.mu.Unlock()

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
	f.mu.Unlock()

	if err := f.Record("delete-network:" + name); err != nil {
		writeAPIError(w, 500, err.Error())
		return
	}

	f.mu.Lock()
	delete(f.networks, id)
	f.mu.Unlock()

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
