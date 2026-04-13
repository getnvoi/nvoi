package handlers_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetProvider_RejectsLocalBuild(t *testing.T) {
	r, _ := testRouter(t, "alice")
	token, _, wsID := doLogin(t, r, "alice")

	body := map[string]string{
		"alias":       "local-build",
		"kind":        "build",
		"provider":    "local",
		"credentials": "{}",
	}
	req := authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetProvider_AllowsDaytonaBuild(t *testing.T) {
	r, _ := testRouter(t, "bob")
	token, _, wsID := doLogin(t, r, "bob")

	body := map[string]string{
		"alias":       "daytona-team",
		"kind":        "build",
		"provider":    "daytona",
		"credentials": `{"api_key":"xxx"}`,
	}
	req := authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetProvider_MultipleAliasesSameProvider(t *testing.T) {
	r, _ := testRouter(t, "carol")
	token, _, wsID := doLogin(t, r, "carol")

	// Add two hetzner compute providers with different aliases.
	for _, alias := range []string{"hetzner-prod", "hetzner-staging"} {
		body := map[string]string{
			"alias":       alias,
			"kind":        "compute",
			"provider":    "hetzner",
			"credentials": `{"token":"xxx"}`,
		}
		req := authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), body, token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("alias %s: expected 200, got %d: %s", alias, w.Code, w.Body.String())
		}
	}

	// List — should have both.
	req := authRequest("GET", fmt.Sprintf("/workspaces/%s/providers", wsID), nil, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var providers []struct {
		Alias    string `json:"alias"`
		Kind     string `json:"kind"`
		Provider string `json:"provider"`
	}
	decode(t, w, &providers)

	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}
}

func TestSetProvider_UpsertByAlias(t *testing.T) {
	r, _ := testRouter(t, "dave")
	token, _, wsID := doLogin(t, r, "dave")

	// Create.
	body := map[string]string{
		"alias":       "my-hetzner",
		"kind":        "compute",
		"provider":    "hetzner",
		"credentials": `{"token":"old"}`,
	}
	req := authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var createResp struct{ Created bool }
	decode(t, w, &createResp)
	if !createResp.Created {
		t.Error("first call should return created=true")
	}

	// Upsert with same alias — should update, not duplicate.
	body["credentials"] = `{"token":"new"}`
	req = authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), body, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updateResp struct{ Created bool }
	decode(t, w, &updateResp)
	if updateResp.Created {
		t.Error("second call should return created=false")
	}

	// List — should still have exactly one.
	req = authRequest("GET", fmt.Sprintf("/workspaces/%s/providers", wsID), nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var providers []struct{ Alias string }
	decode(t, w, &providers)
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider after upsert, got %d", len(providers))
	}
}

func TestDeleteProvider_ByAlias(t *testing.T) {
	r, _ := testRouter(t, "eve")
	token, _, wsID := doLogin(t, r, "eve")

	// Create.
	body := map[string]string{
		"alias":       "to-delete",
		"kind":        "dns",
		"provider":    "cloudflare",
		"credentials": `{"api_key":"xxx"}`,
	}
	req := authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), body, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Delete by alias.
	req = authRequest("DELETE", fmt.Sprintf("/workspaces/%s/providers/to-delete", wsID), nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// List — should be empty.
	req = authRequest("GET", fmt.Sprintf("/workspaces/%s/providers", wsID), nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var providers []struct{ Alias string }
	decode(t, w, &providers)
	if len(providers) != 0 {
		t.Fatalf("expected 0 providers after delete, got %d", len(providers))
	}
}

func TestRepoLinkProvider_ByAlias(t *testing.T) {
	r, _ := testRouter(t, "frank")
	token, _, wsID := doLogin(t, r, "frank")

	// Create provider.
	provBody := map[string]string{
		"alias":       "hz-prod",
		"kind":        "compute",
		"provider":    "hetzner",
		"credentials": `{"token":"xxx"}`,
	}
	req := authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), provBody, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create provider: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Create repo.
	repoBody := map[string]string{"name": "myapp"}
	req = authRequest("POST", fmt.Sprintf("/workspaces/%s/repos", wsID), repoBody, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create repo: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo struct {
		ID string `json:"id"`
	}
	decode(t, w, &repo)

	// Link provider by alias.
	linkBody := map[string]string{"compute_provider": "hz-prod"}
	req = authRequest("PUT", fmt.Sprintf("/workspaces/%s/repos/%s", wsID, repo.ID), linkBody, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("link provider: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify link — get repo and check compute_provider_id is set.
	req = authRequest("GET", fmt.Sprintf("/workspaces/%s/repos/%s", wsID, repo.ID), nil, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var loaded struct {
		ComputeProviderID *string `json:"compute_provider_id"`
	}
	decode(t, w, &loaded)
	if loaded.ComputeProviderID == nil {
		t.Fatal("compute_provider_id should be set after linking")
	}
}

func TestSetProvider_AliasValidation(t *testing.T) {
	r, _ := testRouter(t, "henry")
	token, _, wsID := doLogin(t, r, "henry")

	invalid := []struct {
		alias string
		why   string
	}{
		{"Hetzner-Prod", "uppercase"},
		{"hetzner prod", "space"},
		{"hetzner_prod", "underscore"},
		{"hetzner.prod", "dot"},
		{"-hetzner", "leading hyphen"},
		{"hetzner-", "trailing hyphen"},
		{"hétzner", "accent"},
		{"hetzner--prod", "double hyphen"},
		{"", "empty"},
	}

	for _, tc := range invalid {
		body := map[string]string{
			"alias":       tc.alias,
			"kind":        "compute",
			"provider":    "hetzner",
			"credentials": `{"token":"xxx"}`,
		}
		req := authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), body, token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code == http.StatusOK {
			t.Errorf("alias %q (%s) should be rejected, got 200", tc.alias, tc.why)
		}
	}

	valid := []string{"hetzner", "hetzner-prod", "cf-dns-1", "a", "hz1"}
	for _, alias := range valid {
		body := map[string]string{
			"alias":       alias,
			"kind":        "compute",
			"provider":    "hetzner",
			"credentials": `{"token":"xxx"}`,
		}
		req := authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), body, token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("alias %q should be valid, got %d: %s", alias, w.Code, w.Body.String())
		}
	}
}

func TestRepoLinkProvider_KindMismatch(t *testing.T) {
	r, _ := testRouter(t, "grace")
	token, _, wsID := doLogin(t, r, "grace")

	// Create a DNS provider.
	provBody := map[string]string{
		"alias":       "cf-dns",
		"kind":        "dns",
		"provider":    "cloudflare",
		"credentials": `{"api_key":"xxx"}`,
	}
	req := authRequest("POST", fmt.Sprintf("/workspaces/%s/providers", wsID), provBody, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create provider: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Create repo.
	repoBody := map[string]string{"name": "myapp"}
	req = authRequest("POST", fmt.Sprintf("/workspaces/%s/repos", wsID), repoBody, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var repo struct {
		ID string `json:"id"`
	}
	decode(t, w, &repo)

	// Try to link DNS provider as compute — should fail.
	linkBody := map[string]string{"compute_provider": "cf-dns"}
	req = authRequest("PUT", fmt.Sprintf("/workspaces/%s/repos/%s", wsID, repo.ID), linkBody, token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for kind mismatch, got %d: %s", w.Code, w.Body.String())
	}
}
