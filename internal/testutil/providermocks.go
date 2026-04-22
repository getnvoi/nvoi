// Package testutil — provider fakes.
//
// ─────────────────────────────────────────────────────────────────────────────
// GOVERNANCE — READ BEFORE EDITING OR ADDING A MOCK
// ─────────────────────────────────────────────────────────────────────────────
//
// Single pattern, single package. Every provider-boundary mock in the nvoi
// test suite lives in this package and follows the same shape.
//
// Rules (hard):
//
//   1. No test declares a type that implements ComputeProvider / DNSProvider /
//      BucketProvider. Ever. The real provider clients (hetzner.Client,
//      cloudflare.DNSClient, cloudflare.Client) are exercised end-to-end
//      against httptest.Server. If you're tempted to write `type myMock
//      struct { ... }` in a _test.go file, stop — extend the fake here.
//
//   2. Tests seed state (SeedServer, SeedVolume, SeedFirewall, SeedNetwork,
//      SeedDNSRecord, SeedBucket). Tests never stub behavior (no func hooks
//      that replace a handler branch). If a new behavior is needed, it lives
//      in the fake's HTTP handler — one place.
//
//   3. The OpLog is the assertion surface. Tests call fake.Has / fake.Count /
//      fake.IndexOf / fake.All. Tests do not poke HTTP request history
//      directly. If a new op is needed, the handler records it.
//
//   4. Error injection is explicit: ErrorOn("delete-firewall:<name>", err).
//      The matching HTTP handler short-circuits to a 500 with the error
//      message. No `if testMode then ...` branches anywhere.
//
//   5. Register binds the fake to a named provider in the global registry.
//      The factory constructs a real provider client and points it at the
//      fake's URL. One line in each test.
//
//   6. MockSSH (pkg-level, internal/testutil/mock_ssh.go) and
//      kubefake.KubeFake stay. Those are at correct boundaries (SSH protocol,
//      client-go fake). Do NOT add SSH or Kube fakes here.
//
//   7. MockOutput stays in mock_provider.go — it's an internal UI contract,
//      not an external boundary.
//
//   8. If you feel the urge to add a parallel fake variant "because the test
//      is different" — it's not. Extend this fake.
//
// Violations of these rules should be reverted in review. The whole point of
// this package is to eliminate the drift-prone class-rewrite pattern that
// testutil.MockCompute / MockDNS / MockBucket used to be.
// ─────────────────────────────────────────────────────────────────────────────

package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// Cleanup is the minimal interface fakes need for automatic close-on-test-end.
// *testing.T satisfies it via t.Cleanup(func()).
// Tests that want no auto-close can pass nil and manage Close() themselves.
type Cleanup interface {
	Cleanup(func())
}

// ── OpLog ─────────────────────────────────────────────────────────────────────

// OpLog records every semantic operation a fake performs so tests can assert
// against a flat string list. Shared across all fakes in this package.
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

// ── Shared error writers ──────────────────────────────────────────────────────

// writeAPIError is for Hetzner API shape.
func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": "error", "message": msg},
	})
}

// writeCFError is for Cloudflare API shape.
func writeCFError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"errors":  []map[string]any{{"code": status, "message": msg}},
	})
}

// writeS3Error is for S3 XML shape.
func writeS3Error(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<Error><Message>%s</Message></Error>`, msg)
}

// writeGHError is for GitHub API shape.
func writeGHError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": msg})
}

// ── Provider registration helpers ─────────────────────────────────────────────

// registerInfra is a shared helper for infra fake registration.
func registerInfra(name string, factory func(creds map[string]string) provider.InfraProvider) {
	schema := provider.CredentialSchema{Name: name}
	provider.RegisterInfra(name, schema, factory)
}

// registerDNS is a shared helper for DNS fake registration.
func registerDNS(name string, factory func(creds map[string]string) provider.DNSProvider) {
	schema := provider.CredentialSchema{Name: name}
	provider.RegisterDNS(name, schema, factory)
}

// registerBucket is a shared helper for bucket fake registration.
func registerBucket(name string, factory func(creds map[string]string) provider.BucketProvider) {
	schema := provider.CredentialSchema{Name: name}
	provider.RegisterBucket(name, schema, factory)
}

// registerCI is a shared helper for CI fake registration.
func registerCI(name string, schema provider.CredentialSchema, factory func(creds map[string]string) provider.CIProvider) {
	provider.RegisterCI(name, schema, factory)
}
