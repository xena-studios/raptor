package dns

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// fakeCloudflare is the DNS records part of Cloudflare's API for one zone.
type fakeCloudflare struct {
	mu      sync.Mutex
	records map[string]cfRecord
	next    int
	calls   []string
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method)
	reply := func(result any) {
		b, _ := json.Marshal(result)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": json.RawMessage(b)})
	}
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 10000, "message": "Authentication error"}}})
		return
	}
	base := "/zones/zone1/dns_records"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == base:
		out := []cfRecord{}
		for _, rec := range f.records {
			if rec.Type == r.URL.Query().Get("type") && rec.Name == r.URL.Query().Get("name") {
				out = append(out, rec)
			}
		}
		reply(out)
	case r.Method == http.MethodPost && r.URL.Path == base:
		var rec cfRecord
		_ = json.NewDecoder(r.Body).Decode(&rec)
		f.next++
		rec.ID = fmt.Sprint(f.next)
		f.records[rec.ID] = rec
		reply(rec)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, base+"/"):
		var rec cfRecord
		_ = json.NewDecoder(r.Body).Decode(&rec)
		rec.ID = strings.TrimPrefix(r.URL.Path, base+"/")
		f.records[rec.ID] = rec
		reply(rec)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, base+"/"):
		delete(f.records, strings.TrimPrefix(r.URL.Path, base+"/"))
		reply(map[string]string{})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeCloudflare) get(name, typ string) []cfRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cfRecord
	for _, r := range f.records {
		if r.Name == name && r.Type == typ {
			out = append(out, r)
		}
	}
	return out
}

func TestCloudflare(t *testing.T) {
	f := &fakeCloudflare{records: map[string]cfRecord{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	cf := &Cloudflare{Token: "tok", ZoneID: "zone1", API: srv.URL}
	ctx := context.Background()
	name := "n-abcd2345.raptornodes.net"

	if err := cf.Set(ctx, name, "A", netip.MustParseAddr("203.0.113.7")); err != nil {
		t.Fatal(err)
	}
	r := f.get(name, "A")
	if len(r) != 1 || r[0].Content != "203.0.113.7" || r[0].Proxied || r[0].TTL != TTL {
		t.Fatalf("created: %+v", r)
	}
	// A new address updates the record in place.
	if err := cf.Set(ctx, name, "A", netip.MustParseAddr("203.0.113.8")); err != nil {
		t.Fatal(err)
	}
	if r := f.get(name, "A"); len(r) != 1 || r[0].Content != "203.0.113.8" {
		t.Fatalf("updated: %+v", r)
	}
	// Duplicates are cleaned up.
	f.records["99"] = cfRecord{ID: "99", Type: "A", Name: name, Content: "198.51.100.1"}
	if err := cf.Set(ctx, name, "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatal(err)
	}
	if r := f.get(name, "A"); len(r) != 1 || r[0].Content != "203.0.113.9" {
		t.Fatalf("deduplicated: %+v", r)
	}
	// AAAA beside it, then removing it.
	if err := cf.Set(ctx, name, "AAAA", netip.MustParseAddr("2001:db8::1")); err != nil {
		t.Fatal(err)
	}
	if err := cf.Set(ctx, name, "AAAA", netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	if r := f.get(name, "AAAA"); len(r) != 0 {
		t.Fatalf("removed: %+v", r)
	}
	if len(f.get(name, "A")) != 1 {
		t.Fatal("removing AAAA touched A")
	}
	// API errors come back with Cloudflare's message.
	bad := &Cloudflare{Token: "wrong", ZoneID: "zone1", API: srv.URL}
	if err := bad.Set(ctx, name, "A", netip.MustParseAddr("203.0.113.7")); err == nil || !strings.Contains(err.Error(), "Authentication error") {
		t.Errorf("bad token: %v", err)
	}
}
