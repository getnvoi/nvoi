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
	"github.com/getnvoi/nvoi/pkg/provider/neon"
)

type NeonFake struct {
	*httptest.Server
	*OpLog

	mu       sync.Mutex
	projects map[string]*neonFakeProject
	nextID   int
}

type neonFakeProject struct {
	ID            string
	Name          string
	DefaultBranch string
	Host          string
	RoleName      string
	Password      string
	DatabaseName  string
	Branches      map[string]*neonFakeBranch
}

type neonFakeBranch struct {
	ID        string
	Name      string
	CreatedAt string
}

func NewNeonFake(t Cleanup) *NeonFake {
	f := &NeonFake{
		OpLog:    NewOpLog(),
		projects: map[string]*neonFakeProject{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	if t != nil {
		t.Cleanup(func() { f.Server.Close() })
	}
	return f
}

func (f *NeonFake) Register(name string) {
	schema := neon.Schema
	registerDatabase(name, schema, func(creds map[string]string) provider.DatabaseProvider {
		clone := map[string]string{}
		for k, v := range creds {
			clone[k] = v
		}
		clone["base_url"] = f.URL
		return neon.New(clone)
	})
}

func (f *NeonFake) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/projects":
		f.listProjects(w)
	case r.Method == http.MethodPost && r.URL.Path == "/projects":
		f.createProject(w, r)
	case strings.HasPrefix(r.URL.Path, "/projects/") && strings.HasSuffix(r.URL.Path, "/branches") && r.Method == http.MethodGet:
		f.listBranches(w, r)
	case strings.HasPrefix(r.URL.Path, "/projects/") && strings.HasSuffix(r.URL.Path, "/branches") && r.Method == http.MethodPost:
		f.createBranch(w, r)
	case strings.HasPrefix(r.URL.Path, "/projects/") && strings.Contains(r.URL.Path, "/branches/") && strings.Contains(r.URL.Path, "/roles/") && strings.HasSuffix(r.URL.Path, "/reveal_password"):
		f.revealPassword(w, r)
	case strings.HasPrefix(r.URL.Path, "/projects/") && strings.Contains(r.URL.Path, "/branches/") && strings.HasSuffix(r.URL.Path, "/dump"):
		f.dumpBranch(w, r)
	case strings.HasPrefix(r.URL.Path, "/projects/") && strings.Contains(r.URL.Path, "/branches/"):
		f.getBranch(w, r)
	case strings.HasPrefix(r.URL.Path, "/projects/") && r.Method == http.MethodDelete:
		f.deleteProject(w, r)
	case r.URL.Path == "/sql" && r.Method == http.MethodPost:
		f.sql(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *NeonFake) listProjects(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, p := range f.projects {
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "default_branch_id": p.DefaultBranch})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"projects": out})
}

func (f *NeonFake) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("proj-%d", f.nextID)
	branchID := fmt.Sprintf("br-%d", f.nextID)
	p := &neonFakeProject{
		ID:            id,
		Name:          body.Project.Name,
		DefaultBranch: branchID,
		Host:          "db.neon.fake",
		RoleName:      "neon_user",
		Password:      "neon_password",
		DatabaseName:  "neondb",
		Branches: map[string]*neonFakeBranch{
			branchID: {ID: branchID, Name: "main", CreatedAt: time.Now().UTC().Format(time.RFC3339)},
		},
	}
	f.projects[id] = p
	_ = f.Record("create-project:" + p.Name)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"project": map[string]any{"id": p.ID, "name": p.Name},
		"branch":  map[string]any{"id": branchID, "host": p.Host, "role_name": p.RoleName, "database_name": p.DatabaseName, "created_at": p.Branches[branchID].CreatedAt},
	})
}

func (f *NeonFake) getBranch(w http.ResponseWriter, r *http.Request) {
	projectID, branchID := projectBranchIDs(r.URL.Path)
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[projectID]
	if p == nil || p.Branches[branchID] == nil {
		http.NotFound(w, r)
		return
	}
	b := p.Branches[branchID]
	_ = json.NewEncoder(w).Encode(map[string]any{"branch": map[string]any{"id": b.ID, "host": p.Host, "role_name": p.RoleName, "database_name": p.DatabaseName, "created_at": b.CreatedAt}})
}

func (f *NeonFake) listBranches(w http.ResponseWriter, r *http.Request) {
	projectID := strings.Split(strings.TrimPrefix(r.URL.Path, "/projects/"), "/")[0]
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[projectID]
	if p == nil {
		http.NotFound(w, r)
		return
	}
	var out []map[string]any
	for _, b := range p.Branches {
		out = append(out, map[string]any{"id": b.ID, "name": b.Name, "created_at": b.CreatedAt})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"branches": out})
}

func (f *NeonFake) createBranch(w http.ResponseWriter, r *http.Request) {
	projectID := strings.Split(strings.TrimPrefix(r.URL.Path, "/projects/"), "/")[0]
	var body struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[projectID]
	if p == nil {
		http.NotFound(w, r)
		return
	}
	f.nextID++
	id := fmt.Sprintf("br-%d", f.nextID)
	b := &neonFakeBranch{ID: id, Name: body.Branch.Name, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	p.Branches[id] = b
	_ = f.Record("create-branch:" + body.Branch.Name)
	_ = json.NewEncoder(w).Encode(map[string]any{"branch": map[string]any{"id": b.ID, "name": b.Name, "host": p.Host, "role_name": p.RoleName, "database_name": p.DatabaseName, "created_at": b.CreatedAt}})
}

func (f *NeonFake) revealPassword(w http.ResponseWriter, r *http.Request) {
	projectID := strings.Split(strings.TrimPrefix(r.URL.Path, "/projects/"), "/")[0]
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[projectID]
	if p == nil {
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"password": p.Password})
}

func (f *NeonFake) dumpBranch(w http.ResponseWriter, r *http.Request) {
	_, branchID := projectBranchIDs(r.URL.Path)
	_, _ = w.Write([]byte("-- dump for " + branchID))
}

func (f *NeonFake) deleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimPrefix(r.URL.Path, "/projects/")
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.projects, projectID)
	w.WriteHeader(http.StatusNoContent)
}

func (f *NeonFake) sql(w http.ResponseWriter, r *http.Request) {
	_ = f.Record("sql")
	_ = json.NewEncoder(w).Encode(provider.SQLResult{Columns: []string{"n"}, Rows: [][]string{{"1"}}, RowsAffected: 1})
}

func projectBranchIDs(path string) (string, string) {
	parts := strings.Split(strings.TrimPrefix(path, "/projects/"), "/")
	if len(parts) < 3 {
		return "", ""
	}
	return parts[0], parts[2]
}
