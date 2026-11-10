package dns

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// AWS's own example (Signature Version 4 docs, "GET IAM ListUsers"): the
// signer must produce exactly its signature.
func TestSignV4KnownAnswer(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	signV4(req, nil, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "us-east-1", "iam",
		time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request, " +
		"SignedHeaders=content-type;host;x-amz-date, " +
		"Signature=5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization =\n %s\nwant\n %s", got, want)
	}
}

// fakeRoute53 is the record sets part of Route 53's API for one zone.
type fakeRoute53 struct {
	mu   sync.Mutex
	sets map[string]r53RecordSet // name+" "+type
}

func (f *fakeRoute53) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKID/") {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<ErrorResponse><Error><Code>AccessDenied</Code><Message>no</Message></Error></ErrorResponse>`)
		return
	}
	base := "/2013-04-01/hostedzone/Z1/rrset"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == base:
		name, typ := r.URL.Query().Get("name"), r.URL.Query().Get("type")
		var out r53ListResponse
		if s, ok := f.sets[name+" "+typ]; ok {
			out.Sets = append(out.Sets, s)
		} else {
			// Route 53 answers with the next set instead.
			out.Sets = append(out.Sets, r53RecordSet{Name: "zz.raptornodes.net.", Type: "A"})
		}
		b, _ := xml.Marshal(struct {
			XMLName xml.Name `xml:"ListResourceRecordSetsResponse"`
			r53ListResponse
		}{r53ListResponse: out})
		_, _ = w.Write(b)
	case r.Method == http.MethodPost && r.URL.Path == base+"/":
		var req r53ChangeRequest
		if err := xml.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Changes) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c := req.Changes[0]
		k := c.Set.Name + " " + c.Set.Type
		switch c.Action {
		case "UPSERT":
			f.sets[k] = c.Set
		case "DELETE":
			if _, ok := f.sets[k]; !ok {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `<ErrorResponse><Error><Code>InvalidChangeBatch</Code><Message>record set not found</Message></Error></ErrorResponse>`)
				return
			}
			delete(f.sets, k)
		}
		_, _ = io.WriteString(w, `<ChangeResourceRecordSetsResponse/>`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestRoute53(t *testing.T) {
	f := &fakeRoute53{sets: map[string]r53RecordSet{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	r := &Route53{AccessKeyID: "AKID", SecretAccessKey: "s", ZoneID: "Z1", Endpoint: srv.URL}
	ctx := context.Background()
	name := "1a2b3c4d5e6f.raptornodes.net"
	key := name + ". A"

	if err := r.Set(ctx, name, "A", netip.MustParseAddr("203.0.113.7")); err != nil {
		t.Fatal(err)
	}
	if s := f.sets[key]; s.TTL != TTL || len(s.ResourceRecords) != 1 || s.ResourceRecords[0].Value != "203.0.113.7" {
		t.Fatalf("created: %+v", s)
	}
	// A new address replaces it.
	if err := r.Set(ctx, name, "A", netip.MustParseAddr("203.0.113.8")); err != nil {
		t.Fatal(err)
	}
	if s := f.sets[key]; len(s.ResourceRecords) != 1 || s.ResourceRecords[0].Value != "203.0.113.8" {
		t.Fatalf("updated: %+v", s)
	}
	// AAAA beside it, then removing it (an invalid address), leaves A alone.
	if err := r.Set(ctx, name, "AAAA", netip.MustParseAddr("2001:db8::1")); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ctx, name, "AAAA", netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.sets[name+". AAAA"]; ok {
		t.Fatal("AAAA left after removing it")
	}
	if _, ok := f.sets[key]; !ok {
		t.Fatal("removing AAAA touched A")
	}
	if err := r.Remove(ctx, name, "A"); err != nil {
		t.Fatal(err)
	}
	if len(f.sets) != 0 {
		t.Fatalf("left: %+v", f.sets)
	}
	// Nothing there: nothing to do, not an error.
	if err := r.Remove(ctx, name, "A"); err != nil {
		t.Fatalf("removing what isn't there: %v", err)
	}
	if err := r.Remove(ctx, name, "TXT"); err == nil {
		t.Error("removed a TXT record")
	}
	// The wildcard and apex are aliases: never deleted.
	f.sets["x.raptornodes.net. A"] = r53RecordSet{Name: "x.raptornodes.net.", Type: "A", AliasTarget: &struct {
		DNSName string `xml:"DNSName"`
	}{DNSName: "d111.cloudfront.net."}}
	if err := r.Remove(ctx, "x.raptornodes.net", "A"); err == nil {
		t.Error("deleted an alias record")
	}
	// API errors come back with Route 53's code.
	bad := &Route53{AccessKeyID: "wrong", SecretAccessKey: "s", ZoneID: "Z1", Endpoint: srv.URL}
	if err := bad.Set(ctx, name, "A", netip.MustParseAddr("203.0.113.7")); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("bad key: %v", err)
	}
}
