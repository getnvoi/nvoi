package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/ngrok"
)

// NgrokFake is a stateful in-memory ngrok API.
// Covers reserved_domains: list, create, delete.
type NgrokFake struct {
	*httptest.Server
	*OpLog

	mu      sync.Mutex
	seqID   int
	domains map[string]*ngrokFakeDomain // id → domain
}

type ngrokFakeDomain struct {
	ID          string
	Domain      string
	CNAMETarget string
	Metadata    string
}

// NewNgrokFake creates a running ngrok fake.
func NewNgrokFake(t *testing.T) *NgrokFake {
	f := &NgrokFake{
		OpLog:   NewOpLog(),
		domains: map[string]*ngrokFakeDomain{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() { f.Server.Close() })
	return f
}

// Register binds this fake to the tunnel registry under name.
func (f *NgrokFake) Register(name string) {
	schema := provider.CredentialSchema{Name: name}
	provider.RegisterTunnel(name, schema, func(creds map[string]string) provider.TunnelProvider {
		c := ngrok.NewClient(map[string]string{
			"api_key":   "test-api-key",
			"authtoken": "test-authtoken",
		})
		c.APIClient().BaseURL = f.URL
		return c
	})
}

// SeedDomain inserts a reserved domain into the fake.
func (f *NgrokFake) SeedDomain(id, domain, cnameTarget string) *ngrokFakeDomain {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := &ngrokFakeDomain{ID: id, Domain: domain, CNAMETarget: cnameTarget}
	f.domains[id] = d
	return d
}

func (f *NgrokFake) SeedDomainWithMetadata(id, domain, cnameTarget, metadata string) *ngrokFakeDomain {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := &ngrokFakeDomain{ID: id, Domain: domain, CNAMETarget: cnameTarget, Metadata: metadata}
	f.domains[id] = d
	return d
}

func (f *NgrokFake) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/reserved_domains" && r.Method == "GET":
		f.handleList(w)
	case path == "/reserved_domains" && r.Method == "POST":
		f.handleCreate(w, r)
	case strings.HasPrefix(path, "/reserved_domains/") && r.Method == "DELETE":
		id := strings.TrimPrefix(path, "/reserved_domains/")
		f.handleDelete(w, id)
	default:
		http.Error(w, "not found", 404)
	}
}

func (f *NgrokFake) handleList(w http.ResponseWriter) {
	_ = f.Record("list-domains")
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.domains))
	for _, d := range f.domains {
		out = append(out, map[string]any{
			"id":           d.ID,
			"domain":       d.Domain,
			"cname_target": d.CNAMETarget,
			"metadata":     d.Metadata,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"reserved_domains": out})
}

func (f *NgrokFake) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain   string `json:"domain"`
		Metadata string `json:"metadata"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := f.Record("create-domain:" + body.Domain); err != nil {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]any{"error_code": 500, "msg": err.Error()})
		return
	}
	f.mu.Lock()
	f.seqID++
	id := fmt.Sprintf("rd-%d", f.seqID)
	d := &ngrokFakeDomain{
		ID:          id,
		Domain:      body.Domain,
		CNAMETarget: body.Domain + ".cname.ngrok.io",
		Metadata:    body.Metadata,
	}
	f.domains[id] = d
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":           id,
		"domain":       body.Domain,
		"cname_target": body.Domain + ".cname.ngrok.io",
		"metadata":     body.Metadata,
	})
}

func (f *NgrokFake) handleDelete(w http.ResponseWriter, id string) {
	f.mu.Lock()
	d, ok := f.domains[id]
	if ok {
		delete(f.domains, id)
	}
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(404)
		return
	}
	_ = f.Record("delete-domain:" + d.Domain)
	w.WriteHeader(204)
}
