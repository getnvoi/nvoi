package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/handlers"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func init() {
	os.Setenv("JWT_SECRET", "test-secret")
	// 32 bytes hex-encoded for AES-256-GCM (RepoConfig env encryption).
	os.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	_ = api.InitEncryption()
	gin.SetMode(gin.TestMode)
}

// fakeVerifier always succeeds and returns the given username.
func fakeVerifier(username string) api.GitHubVerifier {
	return func(pat string) (*api.GitHubUser, error) {
		return &api.GitHubUser{Login: username}, nil
	}
}

// testRouter returns a gin engine wired with a fresh in-memory DB.
func testRouter(t *testing.T, username string) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := api.TestDB()
	r := handlers.NewRouter(db, fakeVerifier(username))
	return r, db
}

// doLogin logs in and returns the JWT token + user ID + default workspace ID.
func doLogin(t *testing.T, r *gin.Engine, username string) (token, userID, workspaceID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"github_token": "fake"})
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login: got %d, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Token string `json:"token"`
		User  struct {
			ID string `json:"id"`
		} `json:"user"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return resp.Token, resp.User.ID, resp.Workspace.ID
}

// authRequest creates an HTTP request with the Bearer token set.
func authRequest(method, path string, body any, token string) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// newRouterWithDB creates a router using an existing DB — for multi-user tests
// where two users share the same database.
func newRouterWithDB(db *gorm.DB, username string) *gin.Engine {
	return handlers.NewRouter(db, fakeVerifier(username))
}

// decode unmarshals the response body into dest.
func decode(t *testing.T, w *httptest.ResponseRecorder, dest any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dest); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, w.Body.String())
	}
}
