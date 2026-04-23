package testutil

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/cloudflare"
)

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
	registerDNS(name, func(creds map[string]string) provider.DNSProvider {
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
	registerBucket(name, func(creds map[string]string) provider.BucketProvider {
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

// HasBucket reports whether the fake currently holds a bucket with the
// given name. Used by reconcile-level tests that assert implicit bucket
// provisioning (e.g. database backup buckets).
func (f *CloudflareFake) HasBucket(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.buckets[name]
	return ok
}

// BucketNames returns every bucket held by the fake, for test error
// messages that want to show what actually exists when an expectation
// fails.
func (f *CloudflareFake) BucketNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.buckets))
	for name := range f.buckets {
		out = append(out, name)
	}
	return out
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
	f.mu.Unlock()

	if err := f.Record("delete-dns:" + name); err != nil {
		writeCFError(w, 500, err.Error())
		return
	}

	f.mu.Lock()
	delete(f.records, id)
	f.mu.Unlock()

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
	f.mu.Unlock()

	if err := f.Record("delete-bucket:" + name); err != nil {
		writeCFError(w, 500, err.Error())
		return
	}

	f.mu.Lock()
	delete(f.buckets, name)
	f.mu.Unlock()

	_ = json.NewEncoder(w).Encode(map[string]any{"result": nil, "success": true})
}

// ── CloudflareFake: S3 handlers ──

func (f *CloudflareFake) handleS3List(w http.ResponseWriter, r *http.Request, bucket string) {
	// Record "empty-bucket:X" on the initial list — EmptyBucket always starts
	// with GET ?list-type=2, even when the bucket is empty.
	if err := f.Record("empty-bucket:" + bucket); err != nil {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(500)
		_, _ = fmt.Fprintf(w, `<Error><Message>%s</Message></Error>`, err.Error())
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
