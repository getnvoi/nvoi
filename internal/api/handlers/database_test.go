package handlers_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createRepoForDB(t *testing.T, r http.Handler, token, wsID string) string {
	t.Helper()
	body := map[string]string{"name": "dbapp"}
	req := authRequest("POST", fmt.Sprintf("/workspaces/%s/repos", wsID), body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create repo: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo struct {
		ID string `json:"id"`
	}
	decode(t, w, &repo)
	return repo.ID
}

func TestDatabaseBackupList_NoProvider(t *testing.T) {
	r, _ := testRouter(t, "dbuser1")
	token, _, wsID := doLogin(t, r, "dbuser1")
	repoID := createRepoForDB(t, r, token, wsID)

	req := authRequest("GET", fmt.Sprintf("/workspaces/%s/repos/%s/database/backups", wsID, repoID), nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should fail — no compute provider means no cluster to SSH into
	if w.Code == http.StatusOK {
		t.Fatalf("expected error without compute provider, got 200")
	}
}

func TestDatabaseSQL_NoProvider(t *testing.T) {
	r, _ := testRouter(t, "dbuser2")
	token, _, wsID := doLogin(t, r, "dbuser2")
	repoID := createRepoForDB(t, r, token, wsID)

	body := map[string]string{"query": "SELECT 1"}
	req := authRequest("POST", fmt.Sprintf("/workspaces/%s/repos/%s/database/sql", wsID, repoID), body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected error without compute provider, got 200")
	}
}

func TestDatabaseBackupDownload_NoProvider(t *testing.T) {
	r, _ := testRouter(t, "dbuser3")
	token, _, wsID := doLogin(t, r, "dbuser3")
	repoID := createRepoForDB(t, r, token, wsID)

	req := authRequest("GET", fmt.Sprintf("/workspaces/%s/repos/%s/database/backups/test-backup.sql.gz", wsID, repoID), nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected error without compute provider, got 200")
	}
}

func TestDatabaseEndpoints_Unauthenticated(t *testing.T) {
	r, _ := testRouter(t, "nobody")

	paths := []struct {
		method string
		path   string
	}{
		{"GET", "/workspaces/fake/repos/fake/database/backups"},
		{"GET", "/workspaces/fake/repos/fake/database/backups/key"},
		{"POST", "/workspaces/fake/repos/fake/database/sql"},
	}

	for _, p := range paths {
		req := authRequest(p.method, p.path, nil, "invalid-token")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code == http.StatusOK {
			t.Errorf("%s %s should reject invalid token, got 200", p.method, p.path)
		}
	}
}
