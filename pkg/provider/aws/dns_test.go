package aws

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestResolveDNS(t *testing.T) {
	creds := map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"region":            "us-east-1",
		"zone":              "example.com",
	}
	p, err := provider.ResolveDNS("aws", creds)
	if err != nil {
		t.Fatalf("ResolveDNS with valid creds: %v", err)
	}
	if p == nil {
		t.Fatal("ResolveDNS returned nil provider")
	}
}

func TestResolveDNS_MissingZone(t *testing.T) {
	creds := map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"region":            "us-east-1",
	}
	_, err := provider.ResolveDNS("aws", creds)
	if err == nil {
		t.Fatal("expected error for missing zone")
	}
	if !contains(err.Error(), "zone") {
		t.Errorf("error %q should mention zone", err.Error())
	}
}

func TestEnsureAddress_RemovesCNAMEFirst(t *testing.T) {
	var posts []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/2013-04-01/hostedzonesbyname":
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListHostedZonesByNameResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones>
    <HostedZone>
      <Id>/hostedzone/ZTEST</Id>
      <Name>example.com.</Name>
    </HostedZone>
  </HostedZones>
</ListHostedZonesByNameResponse>`)
		case r.Method == http.MethodGet && r.URL.Path == "/2013-04-01/hostedzone/ZTEST/rrset":
			switch r.URL.Query().Get("type") {
			case "CNAME":
				fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ResourceRecordSets>
    <ResourceRecordSet>
      <Name>app.example.com.</Name>
      <Type>CNAME</Type>
      <TTL>300</TTL>
      <ResourceRecords>
        <ResourceRecord><Value>old.example.net</Value></ResourceRecord>
      </ResourceRecords>
    </ResourceRecordSet>
  </ResourceRecordSets>
</ListResourceRecordSetsResponse>`)
			default:
				fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ResourceRecordSets/>
</ListResourceRecordSetsResponse>`)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/2013-04-01/hostedzone/ZTEST/rrset":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			posts = append(posts, string(body))
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeInfo><Id>/change/C1</Id><Status>PENDING</Status></ChangeInfo>
</ChangeResourceRecordSetsResponse>`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	c := newTestDNSClient(ts)
	if err := c.ensureAddress(context.Background(), "app.example.com", "203.0.113.10"); err != nil {
		t.Fatalf("ensureAddress: %v", err)
	}

	if len(posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(posts))
	}
	if !strings.Contains(posts[0], "<Action>DELETE</Action>") || !strings.Contains(posts[0], "<Type>CNAME</Type>") {
		t.Fatalf("first change = %q, want DELETE CNAME", posts[0])
	}
	if !strings.Contains(posts[1], "<Action>UPSERT</Action>") || !strings.Contains(posts[1], "<Type>A</Type>") {
		t.Fatalf("second change = %q, want UPSERT A", posts[1])
	}
}

func TestEnsureCNAME_RemovesAddressRecordsFirst(t *testing.T) {
	var posts []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/2013-04-01/hostedzonesbyname":
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListHostedZonesByNameResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones>
    <HostedZone>
      <Id>/hostedzone/ZTEST</Id>
      <Name>example.com.</Name>
    </HostedZone>
  </HostedZones>
</ListHostedZonesByNameResponse>`)
		case r.Method == http.MethodGet && r.URL.Path == "/2013-04-01/hostedzone/ZTEST/rrset":
			switch r.URL.Query().Get("type") {
			case "A":
				fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ResourceRecordSets>
    <ResourceRecordSet>
      <Name>app.example.com.</Name>
      <Type>A</Type>
      <TTL>300</TTL>
      <ResourceRecords>
        <ResourceRecord><Value>203.0.113.9</Value></ResourceRecord>
      </ResourceRecords>
    </ResourceRecordSet>
  </ResourceRecordSets>
</ListResourceRecordSetsResponse>`)
			default:
				fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ResourceRecordSets/>
</ListResourceRecordSetsResponse>`)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/2013-04-01/hostedzone/ZTEST/rrset":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			posts = append(posts, string(body))
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeInfo><Id>/change/C1</Id><Status>PENDING</Status></ChangeInfo>
</ChangeResourceRecordSetsResponse>`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	c := newTestDNSClient(ts)
	if err := c.ensureCNAME(context.Background(), "app.example.com", "target.example.net"); err != nil {
		t.Fatalf("ensureCNAME: %v", err)
	}

	if len(posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(posts))
	}
	if !strings.Contains(posts[0], "<Action>DELETE</Action>") || !strings.Contains(posts[0], "<Type>A</Type>") {
		t.Fatalf("first change = %q, want DELETE A", posts[0])
	}
	if !strings.Contains(posts[1], "<Action>UPSERT</Action>") || !strings.Contains(posts[1], "<Type>CNAME</Type>") {
		t.Fatalf("second change = %q, want UPSERT CNAME", posts[1])
	}
}

func TestUnroute_RemovesCNAMEToo(t *testing.T) {
	var posts []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/2013-04-01/hostedzonesbyname":
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListHostedZonesByNameResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones>
    <HostedZone>
      <Id>/hostedzone/ZTEST</Id>
      <Name>example.com.</Name>
    </HostedZone>
  </HostedZones>
</ListHostedZonesByNameResponse>`)
		case r.Method == http.MethodGet && r.URL.Path == "/2013-04-01/hostedzone/ZTEST/rrset":
			recordType := r.URL.Query().Get("type")
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ResourceRecordSets>
    <ResourceRecordSet>
      <Name>app.example.com.</Name>
      <Type>%s</Type>
      <TTL>300</TTL>
      <ResourceRecords>
        <ResourceRecord><Value>%s</Value></ResourceRecord>
      </ResourceRecords>
    </ResourceRecordSet>
  </ResourceRecordSets>
</ListResourceRecordSetsResponse>`, recordType, testValueForType(recordType))
		case r.Method == http.MethodPost && r.URL.Path == "/2013-04-01/hostedzone/ZTEST/rrset":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			posts = append(posts, string(body))
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeInfo><Id>/change/C1</Id><Status>PENDING</Status></ChangeInfo>
</ChangeResourceRecordSetsResponse>`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	c := newTestDNSClient(ts)
	if err := c.Unroute(context.Background(), "app.example.com"); err != nil {
		t.Fatalf("Unroute: %v", err)
	}

	if len(posts) != 3 {
		t.Fatalf("posts = %d, want 3", len(posts))
	}
	for _, rtype := range []string{"A", "AAAA", "CNAME"} {
		found := false
		for _, post := range posts {
			if strings.Contains(post, "<Action>DELETE</Action>") && strings.Contains(post, "<Type>"+rtype+"</Type>") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing delete for %s in %v", rtype, posts)
		}
	}
}

func newTestDNSClient(ts *httptest.Server) *DNSClient {
	cfg := sdkaws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		HTTPClient:  ts.Client(),
	}
	return &DNSClient{
		r53: route53.NewFromConfig(cfg, func(o *route53.Options) {
			o.BaseEndpoint = sdkaws.String(ts.URL)
		}),
		zone: "example.com",
	}
}

func testValueForType(rtype string) string {
	switch rtype {
	case "A":
		return "203.0.113.10"
	case "AAAA":
		return "2001:db8::10"
	default:
		return "target.example.net"
	}
}
