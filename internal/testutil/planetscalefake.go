package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/planetscale"
)

type PlanetScaleFake struct {
	*httptest.Server
	*OpLog

	mu        sync.Mutex
	databases map[string]*psFakeDB
	nextID    int
	org       string
}

type psFakeDB struct {
	Name     string
	Password string
	Branches map[string]string
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
		f.databases[body.Name] = &psFakeDB{Name: body.Name, Password: "ps-pass", Branches: map[string]string{}}
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
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "ps_user", "plain_text": db.Password})
			return
		}
		if len(parts) >= 2 && parts[1] == "branches" && r.Method == http.MethodPost {
			f.nextID++
			id := fmt.Sprintf("branch-%d", f.nextID)
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			db := f.databases[parts[0]]
			if db != nil {
				db.Branches[id] = body.Name
			}
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "name": body.Name, "created_at": time.Now().UTC().Format(time.RFC3339)})
			return
		}
		if len(parts) >= 2 && parts[1] == "branches" && r.Method == http.MethodGet {
			f.mu.Lock()
			db := f.databases[parts[0]]
			f.mu.Unlock()
			if db == nil {
				http.NotFound(w, r)
				return
			}
			var branches []map[string]any
			for id, name := range db.Branches {
				branches = append(branches, map[string]any{"id": id, "name": name, "created_at": time.Now().UTC().Format(time.RFC3339)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"branches": branches})
			return
		}
		if len(parts) == 1 && r.Method == http.MethodDelete {
			f.mu.Lock()
			delete(f.databases, parts[0])
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}
}
