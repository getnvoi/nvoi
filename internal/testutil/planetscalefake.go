package testutil

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/planetscale"
)

// PlanetScaleFake backs two surfaces:
//
//  1. The management API at `/organizations/{org}/databases[/…]` — used
//     by EnsureCredentials to provision the database and mint a scoped
//     role password (the same plane PlanetScale's UI drives).
//  2. The Data API at `/psdb.v1alpha1.Database/Execute` — the gRPC-over-
//     HTTP endpoint the provider hits from ExecSQL. Basic auth from
//     the issued credentials; response shape matches PlanetScale's
//     (lengths + base64-packed values) so the decoder in
//     planetscale.execHTTP round-trips against real and fake alike.
//
// Branch-as-backup endpoints were removed with the unified backup
// pipeline — every DatabaseProvider now dumps to a shared bucket, and
// backup list/download is exercised by s3-level tests, not the provider
// fake.
type PlanetScaleFake struct {
	*httptest.Server
	*OpLog

	mu        sync.Mutex
	databases map[string]*psFakeDB
	org       string
}

type psFakeDB struct {
	Name     string
	Password string
}

func NewPlanetScaleFake(t Cleanup) *PlanetScaleFake {
	f := &PlanetScaleFake{
		OpLog:     NewOpLog(),
		databases: map[string]*psFakeDB{},
		org:       "acme",
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	if t != nil {
		t.Cleanup(func() { f.Server.Close() })
	}
	return f
}

func (f *PlanetScaleFake) Register(name string) {
	schema := planetscale.Schema
	registerDatabase(name, schema, func(creds map[string]string) provider.DatabaseProvider {
		clone := map[string]string{}
		for k, v := range creds {
			clone[k] = v
		}
		clone["organization"] = f.org
		clone["base_url"] = f.URL
		return planetscale.New(clone)
	})
}

func (f *PlanetScaleFake) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/psdb.v1alpha1.Database/Execute" {
		f.handleExecute(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/organizations/"+f.org+"/databases") {
		f.handleDatabases(w, r)
		return
	}
	http.NotFound(w, r)
}

func (f *PlanetScaleFake) handleDatabases(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/organizations/"+f.org+"/databases")
	switch {
	case (path == "" || path == "/") && r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{"databases": []any{}})
	case (path == "" || path == "/") && r.Method == http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.databases[body.Name] = &psFakeDB{Name: body.Name, Password: "ps-pass"}
		f.mu.Unlock()
		_ = f.Record("create-database:" + body.Name)
		_ = json.NewEncoder(w).Encode(map[string]any{"name": body.Name})
	default:
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(parts) >= 2 && parts[1] == "passwords" && r.Method == http.MethodPost {
			f.mu.Lock()
			db := f.databases[parts[0]]
			f.mu.Unlock()
			if db == nil {
				http.NotFound(w, r)
				return
			}
			_ = f.Record("create-password:" + parts[0])
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "ps_user", "plain_text": db.Password})
			return
		}
		if len(parts) == 1 && r.Method == http.MethodDelete {
			f.mu.Lock()
			delete(f.databases, parts[0])
			f.mu.Unlock()
			_ = f.Record("delete-database:" + parts[0])
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}
}

// handleExecute serves the Data API shape. Accepts any non-empty Basic
// auth — credentials are validated against the database object at the
// management-API level, not here — and returns a single-row result for
// `SELECT <n>` patterns so provider tests can assert end-to-end
// round-trip without hard-coding byte layouts.
func (f *PlanetScaleFake) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "Basic ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	_ = f.Record("execute:" + body.Query)

	// Response shape mirrors PlanetScale's: rows encode each field's
	// bytes end-to-end in a single base64 blob, sliced by `lengths`.
	// We echo the SQL statement back as a single row — good enough for
	// the provider's decoder to prove it parses the format correctly.
	value := body.Query
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	length := fmt.Sprintf("%d", len(value))
	resp := map[string]any{
		"result": map[string]any{
			"fields": []map[string]any{
				{"name": "query"},
			},
			"rowsAffected": "1",
			"rows": []map[string]any{
				{"lengths": []string{length}, "values": encoded},
			},
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}
